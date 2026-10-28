-- 0183: allow 'telemetry' as a device-operation source.
--
-- The Live State "Apply field" action (blocker #1, slice 3 -- telemetry/apply.go)
-- records the device operation it logs with source 'telemetry': the model was
-- caught up to a position observed in the FIELD via telemetry, which is a distinct
-- provenance from a crew entry, a raw SCADA event, or a relay trip (the telemetry
-- row's own source may be SCADA, AMI, a sim, ...). The 0147 CHECK didn't include
-- it, so every Apply failed the constraint. Additive: widen the allowed set; no
-- existing row changes. Keep in step with deviceops.sources (models.go).

ALTER TABLE ops.operations DROP CONSTRAINT IF EXISTS operations_source_check;
ALTER TABLE ops.operations ADD CONSTRAINT operations_source_check
    CHECK (source IN ('crew', 'scada', 'relay', 'switching_order', 'derived', 'telemetry', 'other'));
