-- 0168_incident_detection_and_cause.sql
-- Purpose: make an outage SOURCE-AWARE and CAUSE-AWARE as two orthogonal axes.
--
--   detection_source  = HOW we learned (customer call / AMI / SCADA / crew /
--                       planned / upstream / analytics). Until now this was
--                       *inferred* at read time by a fragile heuristic
--                       (isScadaRaised = device-backed + null confidence); now it
--                       is a stored fact stamped by whichever path raises the
--                       incident. detected_at is when the FIRST signal arrived
--                       (a SCADA trip's reported time, the earliest call, ...).
--   cause_code        = WHY it happened, a coded value from a shared catalog
--                       (oms.outage_causes) instead of the old free text. It
--                       starts 'unknown' and is refined (suspected -> confirmed)
--                       as a crew / a raised fault pins it down.
--
-- The two are deliberately separate: a device alarm tells you a protective device
-- operated (source) but not why it operated (cause). Keeping them apart is what
-- lets a utility ask its best maturity question -- "how many outages did we know
-- about before a customer called us?" -- and still slice SAIDI by root cause.
--
-- cause_code stays TEXT (a loose ref into the catalog, not a DB FK) matching the
-- rest of oms's loose coupling; an incident may reference a code that is later
-- deactivated without a cascade. The old free-text cause_code values are kept.

-- --- 1. the coded cause catalog -------------------------------------------
-- Coarse, outage-level cause families. The detailed per-asset FMEA lives on the
-- FAULT (faults.fault_modes); this is the incident-level "why" that is always
-- present, even for an unattributed incident with no fault raised yet.
CREATE TABLE IF NOT EXISTS oms.outage_causes (
    code        text PRIMARY KEY,
    label       text NOT NULL,
    family      text NOT NULL,
    sort_order  integer NOT NULL DEFAULT 100,
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE oms.outage_causes IS 'Coded outage-cause catalog (0168): coarse incident-level "why" (family + code). The detailed per-asset FMEA is on faults.fault_modes; this is the always-present incident cause.';

INSERT INTO oms.outage_causes (code, label, family, sort_order) VALUES
    ('UNKNOWN',                 'Unknown / under investigation', 'unknown',      10),
    ('EQUIPMENT_FAILURE',       'Equipment failure (general)',   'equipment',    20),
    ('TRANSFORMER_FAILURE',     'Transformer failure',           'equipment',    21),
    ('CABLE_FAULT',             'Cable fault',                   'equipment',    22),
    ('CONDUCTOR_DOWN',          'Conductor down',                'equipment',    23),
    ('POLE_DOWN',               'Pole / structure down',         'equipment',    24),
    ('INSULATOR_FAILURE',       'Insulator failure',             'equipment',    25),
    ('THIRD_PARTY_DIGIN',       'Third-party dig-in / excavation','third_party', 30),
    ('VEHICLE_IMPACT',          'Vehicle impact',                'third_party',  31),
    ('VANDALISM_THEFT',         'Vandalism / theft',             'third_party',  32),
    ('LIGHTNING',               'Lightning',                     'environmental',40),
    ('STORM_WIND',              'Storm / high wind',             'environmental',41),
    ('FLOOD',                   'Flood / water',                 'environmental',42),
    ('ICE_SNOW',                'Ice / snow',                    'environmental',43),
    ('HEAT',                    'Extreme heat',                  'environmental',44),
    ('BUSHFIRE',                'Bushfire / wildfire',           'environmental',45),
    ('VEGETATION',              'Vegetation contact',            'vegetation',   50),
    ('ANIMAL',                  'Animal / bird contact',         'animal',       60),
    ('OVERLOAD',                'Overload / thermal',            'overload',     70),
    ('UPSTREAM_SUPPLY',         'Upstream supply loss',          'upstream',     80),
    ('PROTECTION_MISOPERATION', 'Protection mis-operation',      'protection',   85),
    ('PLANNED_WORK',            'Planned work',                  'planned',      90)
ON CONFLICT (code) DO NOTHING;

-- --- 2. the new incident columns ------------------------------------------
ALTER TABLE oms.incidents
    ADD COLUMN IF NOT EXISTS detection_source text,
    ADD COLUMN IF NOT EXISTS detection_detail text,
    ADD COLUMN IF NOT EXISTS detected_at      timestamptz,
    ADD COLUMN IF NOT EXISTS cause_status     text NOT NULL DEFAULT 'unknown';

-- detection_source enum (nullable so a pre-0168 row with no stamp reads as
-- "unknown" rather than forcing a wrong value); cause_status is a small enum.
ALTER TABLE oms.incidents
    DROP CONSTRAINT IF EXISTS oms_incidents_detection_source_chk;
ALTER TABLE oms.incidents
    ADD CONSTRAINT oms_incidents_detection_source_chk
    CHECK (detection_source IS NULL OR detection_source IN
        ('customer', 'ami', 'scada', 'crew', 'planned', 'upstream', 'analytics'));

ALTER TABLE oms.incidents
    DROP CONSTRAINT IF EXISTS oms_incidents_cause_status_chk;
ALTER TABLE oms.incidents
    ADD CONSTRAINT oms_incidents_cause_status_chk
    CHECK (cause_status IN ('unknown', 'suspected', 'confirmed'));

-- --- 3. backfill existing rows --------------------------------------------
-- One-time, best-effort stamp of the source we can no longer observe, using the
-- same heuristic the read path used to infer it (retired for new rows, which are
-- stamped at creation): planned -> 'planned'; an unplanned, device-backed,
-- null-confidence incident is the SCADA auto-raise signature -> 'scada';
-- everything else unplanned came from calls -> 'customer'. detected_at falls back
-- to started_at (the best available earliest time).
UPDATE oms.incidents
   SET detection_source = CASE
         WHEN outage_type = 'planned' THEN 'planned'
         WHEN confidence IS NULL AND predicted_device_id IS NOT NULL THEN 'scada'
         ELSE 'customer'
       END
 WHERE detection_source IS NULL;

UPDATE oms.incidents
   SET detected_at = started_at
 WHERE detected_at IS NULL;

-- A row that already carried a (free-text) cause is treated as a confirmed cause;
-- the rest stay 'unknown'. (Old free-text values may not match a catalog code --
-- that's fine; the UI shows the raw value and lets it be re-coded.)
UPDATE oms.incidents
   SET cause_status = 'confirmed'
 WHERE cause_code IS NOT NULL AND cause_code <> '';

-- --- 4. indexes for the source/cause analytics ----------------------------
CREATE INDEX IF NOT EXISTS oms_incidents_detection_source_idx ON oms.incidents (detection_source);
CREATE INDEX IF NOT EXISTS oms_incidents_cause_code_idx       ON oms.incidents (cause_code);

COMMENT ON COLUMN oms.incidents.detection_source IS 'How the utility learned of the outage (0168): customer|ami|scada|crew|planned|upstream|analytics. Stamped by the raising path, no longer inferred.';
COMMENT ON COLUMN oms.incidents.detected_at IS 'When the FIRST signal arrived (0168): a SCADA trip''s reported time, the earliest trouble call, etc. -- distinct from started_at.';
COMMENT ON COLUMN oms.incidents.cause_status IS 'Cause certainty (0168): unknown -> suspected -> confirmed, refined by a crew / a raised fault.';
