-- 0064_add_feature_class_network.sql
-- Purpose: Phase 37 Slice 2 -- give a feature class a nominal voltage
-- **network** (MV or LV), so the circuit_id picker (and later slices' filter /
-- analysis / stop-at-circuit-boundary) can tell which network a feature belongs
-- to and scope the circuits it may join.
--
-- Why a new column rather than reusing network_role: `network_role` is the
-- feature's role in the connectivity graph (edge / junction / container /
-- none) -- pure topology, unrelated to voltage. Whether a class sits on the
-- MV or the LV system is a separate, orthogonal fact the model didn't record
-- anywhere. The code prefix (MV_/LV_) hints at it but is unreliable: DIST_XFMR,
-- SERVICE_POINT, METER, STREET_LIGHT, PROTECTIVE_RELAY carry no prefix. So the
-- network becomes an explicit, nullable class attribute, matching the `network`
-- column gis.circuits already carries (migration 0062) -- an MV class's circuit
-- picker lists MV circuits, an LV class's lists LV.
--
-- Nullable: structural / cross-network classes (POLE, BAY, SUBSTATION_YARD)
-- and any class not yet classified leave it NULL, and their picker (if any)
-- falls back to showing every circuit grouped by network. Pure metadata with no
-- physical-table footprint (the compiler's DDL never reads it), so like
-- label_def (migration 0055) it just rides along on metadata.Load's struct
-- select and is editable at any class status.
--
-- Populated for the electrical apparatus classes by
-- seed/circuit_membership_example.sql, which also adds the circuit_id field.

ALTER TABLE metadata.feature_classes
    ADD COLUMN network text CHECK (network IS NULL OR network IN ('MV', 'LV'));
