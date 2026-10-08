package webhookout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/logx"
)

// envelope is the JSON body POSTed to a subscriber (design doc §6). It is built
// once per event and shared by every subscription's delivery, so a fan-out
// produces byte-identical bodies -- the per-subscription mapping that would
// reshape it is S4.
type envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Actor      *string         `json:"actor,omitempty"`
	Entity     entityRef       `json:"entity"`
	Data       json.RawMessage `json:"data"`
}

type entityRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func buildEnvelope(ev *OutboundEvent) (json.RawMessage, error) {
	body, err := json.Marshal(envelope{
		EventID:    ev.ID,
		EventType:  ev.EventType,
		OccurredAt: ev.OccurredAt,
		Actor:      ev.ActorID,
		Entity:     entityRef{Kind: ev.EntityKind, ID: ev.EntityID},
		Data:       ev.Payload,
	})
	if err != nil {
		return nil, fmt.Errorf("webhookout: building envelope for event %s: %w", ev.ID, err)
	}
	return body, nil
}

// featureClassOf pulls a feature_class out of an event payload when one is
// present, so a subscription's class_filter can narrow feature-bearing events.
// Operational events (incident/work_order/defect) carry none -> "" -> never
// filtered out.
func featureClassOf(payload json.RawMessage) string {
	var probe struct {
		FeatureClass string `json:"feature_class"`
	}
	_ = json.Unmarshal(payload, &probe)
	return probe.FeatureClass
}

// territoryOf pulls a territory_id out of an event payload when present, so a
// subscription's territory_id filter can scope to it. A feature event carries the
// feature's stored territory_id (Phase 11); operational and geometry-less events
// carry none -> "" -> excluded from any territory-scoped subscription.
func territoryOf(payload json.RawMessage) string {
	var probe struct {
		TerritoryID string `json:"territory_id"`
	}
	_ = json.Unmarshal(payload, &probe)
	return probe.TerritoryID
}

// fanOutOne claims the oldest un-fanned outbox event, inserts one delivery per
// matching active subscription, and stamps fanned_out_at -- all in ONE
// transaction, so the whole step is atomic: a crash before commit rolls back both
// the inserts and the stamp, and the event is cleanly re-fanned; a crash after
// commit leaves fanned_out_at set, so it is never re-fanned. An event that
// matches no subscription is still stamped (recorded, nothing to send). Returns
// (true, nil) when it processed an event, (false, nil) when the outbox was empty.
//
// The claim is plain FOR UPDATE (NOT skip-locked) on purpose: it makes fan-out
// strictly SEQUENTIAL by seq, even across worker replicas. A second worker's
// claim blocks on the oldest row rather than skipping ahead, then re-evaluates to
// the next once this tx commits. That global ordering is what lets the delivery
// stage's per-entity gate be sufficient -- when a delivery exists, every
// earlier-seq event for its entity has already been fanned out, so there is no
// window where a later event is deliverable before an earlier one is even queued.
// Fan-out is a handful of cheap inserts, so serialising it costs nothing that
// matters; the concurrency that counts is the delivery stage, which stays
// SKIP-LOCKED and parallel.
func fanOutOne(ctx context.Context, db *bun.DB) (bool, error) {
	worked := false
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ev := new(OutboundEvent)
		e := tx.NewRaw(`
SELECT * FROM ops.webhook_outbound_events
WHERE fanned_out_at IS NULL
ORDER BY seq
FOR UPDATE
LIMIT 1`).Scan(ctx, ev)
		if errors.Is(e, sql.ErrNoRows) {
			return nil // empty outbox; worked stays false
		}
		if e != nil {
			return fmt.Errorf("webhookout: claiming outbox event: %w", e)
		}
		worked = true

		subs, err := activeSubscriptions(ctx, tx)
		if err != nil {
			return err
		}
		fclass := featureClassOf(ev.Payload)
		tid := territoryOf(ev.Payload)
		body, err := buildEnvelope(ev)
		if err != nil {
			return err
		}

		for i := range subs {
			sub := &subs[i]
			cfg, err := sub.parseConfig()
			if err != nil {
				// A single malformed subscription must not wedge the whole outbox;
				// skip it and let the others through (admin fixes its config).
				logx.From(ctx).Warn("skipping outbound subscription with invalid config",
					"subscription_id", sub.ID, "err", err)
				continue
			}
			if !cfg.matches(ev.EventType, fclass, tid) {
				continue
			}
			// Optional per-subscription mapping (S4): reshape the envelope into the
			// receiver's own shape. A bad/failed mapping falls back to the unmapped
			// envelope with a warning rather than dropping the event -- the admin
			// validated the spec at save time, so this is a belt-and-braces guard.
			deliveryBody := body
			if len(cfg.Mapping) > 0 {
				mapped, err := applyEnvelopeMapping(cfg.Mapping, body)
				if err != nil {
					logx.From(ctx).Warn("outbound mapping failed; sending unmapped envelope",
						"subscription_id", sub.ID, "err", err)
				} else {
					deliveryBody = mapped
				}
			}
			d := &OutboundDelivery{
				SubscriptionID: sub.ID,
				EventID:        ev.ID,
				EventSeq:       ev.Seq,
				EntityKind:     ev.EntityKind,
				EntityID:       ev.EntityID,
				EventType:      ev.EventType,
				Payload:        deliveryBody,
				Status:         "queued",
			}
			if err := insertDelivery(ctx, tx, d); err != nil {
				return err
			}
		}

		if _, err := tx.NewUpdate().Model((*OutboundEvent)(nil)).
			Set("fanned_out_at = now()").
			Where("id = ?", ev.ID).
			Exec(ctx); err != nil {
			return fmt.Errorf("webhookout: stamping fanned_out_at for event %s: %w", ev.ID, err)
		}
		return nil
	})
	return worked, err
}
