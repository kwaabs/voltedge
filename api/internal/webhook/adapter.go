package webhook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// adapter turns one raw change (a single element of the payload's `changes`
// array) into a write against a specific VoltEdge domain. Registered by an
// endpoint's `target`. Each adapter decodes raw into its own typed change shape
// -- so one ingestion pipeline serves feature/reading/work/fault without the
// parser knowing any target's fields. Every adapter runs inside the delivery's
// transaction (tx) and under the endpoint's service-user context (ctx carries
// the *SessionUser via auth.WithUser), so its writes inherit that user's RBAC +
// territory scope and the package's own validation.
type adapter interface {
	apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error)
}

// adapters is the target -> adapter registry. feature is the reference
// implementation; reading is a thin alias of it (a reading is modelled as a
// feature row); work and fault drive the wam/faults engines through their own
// correlation table (ops.webhook_entity_refs, migration 0125).
var adapters = map[string]adapter{
	"feature": featureAdapter{},
	"reading": readingAdapter{},
	"work":    workAdapter{},
	"fault":   faultAdapter{},
}

// featureAdapter ingests feature changes through crud.CreateFeature /
// UpdateFeature / DeleteFeature -- the exact path a hand edit takes, so subtype
// rules, required-attribute checks, territory write-scope, and history all
// apply. Identity is the per-endpoint external_id: a first sight creates and
// records the mapping; a repeat updates the mapped feature (last-writer-wins,
// prior values retained in history); a delete retires it and drops the mapping.
type featureAdapter struct{}

func (featureAdapter) apply(ctx context.Context, tx bun.IDB, ep *Endpoint, raw json.RawMessage) (itemResult, error) {
	var ch nativeChange
	if err := json.Unmarshal(raw, &ch); err != nil {
		return itemResult{}, invalid("invalid feature change: %v", err)
	}
	externalID := strings.TrimSpace(ch.ExternalID)
	if externalID == "" {
		return itemResult{}, invalid("every change needs an external_id (the correlation key)")
	}

	classCode := strings.TrimSpace(ch.FeatureClass)
	if classCode == "" {
		classCode = configString(ep.Config, "feature_class")
	}
	if classCode == "" {
		return itemResult{}, invalid("change %q: feature_class is required (set it on the change or in config.feature_class)", externalID)
	}
	loaded, err := metadata.Load(ctx, tx, classCode)
	if err != nil {
		return itemResult{}, invalid("change %q: unknown feature_class %q: %v", externalID, classCode, err)
	}
	classID := loaded.FeatureClass.ID

	op := defaulted(strings.ToLower(ch.Op), "upsert")

	// Resolve the correlation ref, if this external_id was seen before.
	ref := new(FeatureRef)
	found := true
	err = tx.NewSelect().Model(ref).
		Where("endpoint_id = ?", ep.ID).
		Where("external_id = ?", externalID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		found, err = false, nil
	}
	if err != nil {
		return itemResult{}, fmt.Errorf("webhook: ref lookup: %w", err)
	}

	switch op {
	case "delete":
		if !found {
			// Nothing mapped -- already gone (or never ingested). A no-op, not a
			// failure, so a delete is idempotent.
			return itemResult{Op: "delete", ExternalID: externalID, FeatureClass: classCode, Skipped: true}, nil
		}
		if _, err := crud.DeleteFeature(ctx, tx, loaded, ref.FeatureID); err != nil {
			return itemResult{}, err
		}
		if _, err := tx.NewDelete().Model((*FeatureRef)(nil)).
			Where("endpoint_id = ?", ep.ID).
			Where("external_id = ?", externalID).
			Exec(ctx); err != nil {
			return itemResult{}, fmt.Errorf("webhook: drop ref: %w", err)
		}
		return itemResult{Op: "delete", ExternalID: externalID, FeatureClass: classCode, FeatureID: ref.FeatureID}, nil

	case "upsert":
		if found {
			// Update in place. No ExpectedUpdatedAt -> last-writer-wins; the prior
			// value is preserved in feature history, so an overwrite is reversible.
			if _, err := crud.UpdateFeature(ctx, tx, loaded, ref.FeatureID, crud.UpdateFeatureInput{
				Subtype:     ch.Subtype,
				Name:        ch.Name,
				TerritoryID: ch.TerritoryID,
				Geometry:    ch.Geometry,
				Attributes:  ch.Attributes,
			}); err != nil {
				return itemResult{}, err
			}
			return itemResult{Op: "update", ExternalID: externalID, FeatureClass: classCode, FeatureID: ref.FeatureID}, nil
		}
		row, err := crud.CreateFeature(ctx, tx, loaded, crud.CreateFeatureInput{
			Subtype:     ch.Subtype,
			Name:        ch.Name,
			TerritoryID: ch.TerritoryID,
			Geometry:    ch.Geometry,
			Attributes:  ch.Attributes,
		})
		if err != nil {
			return itemResult{}, err
		}
		newID := stringField(row, "id")
		if newID == "" {
			return itemResult{}, fmt.Errorf("webhook: create returned no id for change %q", externalID)
		}
		if _, err := tx.NewInsert().Model(&FeatureRef{
			EndpointID:     ep.ID,
			ExternalID:     externalID,
			FeatureClassID: classID,
			FeatureID:      newID,
		}).Exec(ctx); err != nil {
			return itemResult{}, fmt.Errorf("webhook: record ref: %w", err)
		}
		return itemResult{Op: "create", ExternalID: externalID, FeatureClass: classCode, FeatureID: newID}, nil

	default:
		return itemResult{}, invalid("change %q: unknown op %q (want upsert or delete)", externalID, ch.Op)
	}
}

// readingAdapter ingests meter/sensor readings. VoltEdge has no separate reading
// store -- a reading is modelled as a feature row (its value/unit/register/read
// timestamp live in the feature's attributes), so this target reuses the feature
// pipeline verbatim: config.feature_class names the reading/metering class, and
// the per-endpoint external_id correlates a source's meter id to the VoltEdge
// feature so a repeat reading updates it in place. It is a thin alias of
// featureAdapter (embedded, so it inherits apply), kept as its own target so an
// integration can declare intent ("this endpoint carries readings") and so a
// future dedicated time-series store could replace it without moving every
// reading endpoint onto a different target.
type readingAdapter struct{ featureAdapter }

// parseChanges normalizes an endpoint's raw payload into a list of raw per-change
// objects, each of which the target adapter decodes into its own typed shape.
// When the endpoint carries a mapping spec, the sender's own JSON is transformed
// into native change objects first (mapping.go). Otherwise the payload is already
// native: either {"changes":[...]} or a single bare change object.
func parseChanges(ep *Endpoint, payload json.RawMessage) ([]json.RawMessage, error) {
	if len(ep.Mapping) > 0 {
		return applyMapping(ep.Mapping, payload)
	}

	// {"changes":[...]} -- a non-nil array (even empty) means the sender used the
	// envelope; applyDelivery rejects an empty batch downstream.
	var env changeEnvelope
	if err := json.Unmarshal(payload, &env); err == nil && env.Changes != nil {
		return env.Changes, nil
	}

	// A single bare change object (no `changes` key) is accepted and wrapped.
	if trimmed := bytes.TrimSpace(payload); len(trimmed) > 0 && trimmed[0] == '{' {
		return []json.RawMessage{payload}, nil
	}

	return nil, invalid(`payload must be a native change object or {"changes": [ ... ]}`)
}

// configString reads a top-level string setting out of an endpoint's config.
func configString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// stringField reads a string-ish column out of a crud result row (uuid columns
// scan back as string, but fall back to fmt for safety).
func stringField(row map[string]any, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
