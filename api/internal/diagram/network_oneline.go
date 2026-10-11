package diagram

// The persisted "generate a network one-line diagram" path was removed: a
// one-line is now an EPHEMERAL, temporary view computed on demand and exported
// (DXF/PDF), never written to runtime.diagram_instances -- so repeatedly looking
// at one leaves no orphaned diagrams behind. The layout + graph read live in
// api/internal/schematic (oneline.go), served by POST /schematic/one-line. This
// package keeps only the real, hand-built/assembly diagram engine.
