package crud

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
)

// Feature event types, aliased crud-local so the write sites (sql.go, publish.go,
// delete.go) stay free of the webhookout import -- it lives only in this file.
const (
	eventFeatureCreated     = webhookout.EventFeatureCreated
	eventFeatureUpdated     = webhookout.EventFeatureUpdated
	eventFeaturePublished   = webhookout.EventFeaturePublished
	eventFeatureUnpublished = webhookout.EventFeatureUnpublished
	eventFeatureDeleted     = webhookout.EventFeatureDeleted
	eventFeatureRestored    = webhookout.EventFeatureRestored
	eventFeaturePurged      = webhookout.EventFeaturePurged
)

// emitFeatureEvent records a feature lifecycle change in the outbound-webhook
// OUTBOX (docs/outbound-webhooks-design.md, S4) -- the sibling of
// recordFeatureHistory, called on the line right after it at each write site and
// in the same best-effort window. On the REST path each statement autocommits, so
// the feature row is already persisted (an Emit error returns like a history-write
// error would); a tx-carrying caller (MCP, field-sync replay) makes the event
// atomic with the change, as the outbox is designed for.
//
// The payload is the feature's formatRow snapshot PLUS its feature_class. The row
// itself carries no class column (it is a per-class physical table), but the class
// is what an outbound subscription's class_filter matches on and what a receiver
// needs to interpret the record -- so it is injected here from loaded. The snapshot
// is copied first so the caller's response map is never mutated. A staged private
// edit never reaches these base writers, so it correctly emits nothing until the
// author applies it (which then runs the ordinary writer and emits the real event).
func emitFeatureEvent(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, eventType string, snapshot map[string]any) error {
	id, _ := snapshot["id"].(string)
	payload := make(map[string]any, len(snapshot)+1)
	for k, v := range snapshot {
		payload[k] = v
	}
	payload["feature_class"] = loaded.FeatureClass.Code
	return webhookout.Emit(ctx, db, eventType, webhookout.KindFeature, id, payload)
}
