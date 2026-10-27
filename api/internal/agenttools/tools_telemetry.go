package agenttools

// The live-state / telemetry tools -- the telemetry surface through the same
// agenttools registry as every other activity. Thin adapters over the already
// exported api/internal/telemetry functions the REST /telemetry routes call,
// gated by exactly the permission each route needs: reconciliation reads
// (telemetry:read), ingest is a machine write (telemetry:ingest), apply is an
// operator write (oms:write -- it sets the model device_state like an operation).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/telemetry"
	"github.com/uptrace/bun"
)

func telemetryTools() []Tool {
	return []Tool{
		{
			Name: "telemetry_reconciliation",
			Description: "Reconcile device TELEMETRY against the MODEL -- the same read the REST GET " +
				"/telemetry/reconciliation route returns. It compares each telemetered device's reported " +
				"state against its model device_state and its reading age against a staleness threshold, " +
				"returning the totals (agreeing / disagreeing / stale / no_model_state) plus the items " +
				"needing attention (disagreements first, then stale). This is how you find where the model " +
				"has drifted from the field. stale_seconds (optional) sets the threshold (default 300).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"stale_seconds":{"type":"integer","description":"Age in seconds past which a reading is stale; default 300."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermTelemetryRead,
			Handler:      toolTelemetryReconciliation,
		},
		{
			Name: "telemetry_apply",
			Description: "Write a device's latest telemetry state onto its MODEL device_state -- the same " +
				"write the REST POST /telemetry/device/{class}/{id}/apply route performs. The operator " +
				"'trust the field' action: correct the model from a disagreeing telemetry reading (surface " +
				"the disagreements with telemetry_reconciliation first). It is logged as a telemetry-sourced " +
				"operation. Returns the device and its new state. This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The device's feature class code."},
					"feature_id":{"type":"string","description":"The device's id (uuid)."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Apply telemetry to model", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &trueHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolTelemetryApply,
		},
		{
			Name: "telemetry_ingest",
			Description: "Ingest a batch of device telemetry samples (latest-value upsert) -- the same write " +
				"the REST POST /telemetry route performs, the SCADA/DMS gateway's feed. Each report names its " +
				"device directly (feature_class + feature_id) OR by external_id + system_code (resolved via " +
				"the external_ref index), with state (OPEN/CLOSED/...), optional quality (good/suspect/stale/" +
				"bad), source and measured_at. A newer measured_at wins; an older one is dropped; a bad report " +
				"is rejected without failing the batch. Returns accepted + rejected counts. This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"reports":{"type":"array","description":"The telemetry samples.","items":{
						"type":"object",
						"properties":{
							"feature_class":{"type":"string"},
							"feature_id":{"type":"string"},
							"external_id":{"type":"string"},
							"system_code":{"type":"string"},
							"state":{"type":"string","description":"Reported switch position, e.g. OPEN / CLOSED."},
							"quality":{"type":"string","enum":["good","suspect","stale","bad"]},
							"source":{"type":"string"},
							"measured_at":{"type":"string","description":"When the field measured it (RFC3339); defaults to now."}
						},
						"additionalProperties":false
					}}
				},
				"required":["reports"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Ingest telemetry", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermTelemetryIngest,
			Handler:      toolTelemetryIngest,
		},
	}
}

func toolTelemetryReconciliation(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		StaleSeconds int `json:"stale_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return telemetry.ReconcileTelemetry(ctx, db, in.StaleSeconds)
}

func toolTelemetryApply(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		FeatureID    string `json:"feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return telemetry.ApplyTelemetry(ctx, sdb, in.FeatureClass, in.FeatureID)
}

func toolTelemetryIngest(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Reports []telemetry.Report `json:"reports"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(in.Reports) == 0 {
		return nil, fmt.Errorf("reports is required (a non-empty array)")
	}
	return telemetry.IngestBatch(ctx, db, in.Reports)
}
