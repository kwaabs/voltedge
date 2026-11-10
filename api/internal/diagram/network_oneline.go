package diagram

// The persisted whole-network operations one-line was REMOVED (again): it tried to
// mirror the live network as an authored GIS diagram (an unowned NETWORK_ONE_LINE
// diagram_instance with diagram_placements, kept current by an incremental graft
// sync), which coupled the operations view to GIS editing and needed constant
// reconciliation to fight layout drift.
//
// The ADMS operations schematic is now a LIVE VIEW instead of a diagram: its layout
// is computed from the current network on demand (schematic.BuildNetworkOneLine,
// served by GET /schematic/network-one-line) and rendered client-side, with operator
// position nudges in an operations-owned override layer (ops.oneline_overrides,
// api/internal/schematic/overrides.go) rather than diagram_placements. So there is
// nothing to generate, sync, or persist here -- this package keeps only the real,
// hand-built/assembly diagram engine.
