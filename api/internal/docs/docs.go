// Package docs serves the VoltEdge product manuals to the in-app documentation
// viewer. It is a thin, read-only file server over a CURATED manifest: the
// request never names a file, it names a manifest id, and the manifest maps
// that id to a fixed on-disk filename -- so there is no path a caller can
// traverse. The Markdown itself lives in the repo-root docs/ directory (the
// same files docs/README.md curates), not in the database and not embedded in
// the binary; DOCS_DIR points at it (default ../docs, matching the server's
// working directory of api/ under `task api-dev`).
//
// Permissioning is per document and enforced HERE, on the server, exactly as
// the caller asked: an administrative manual (the Admin & Break-Glass guide,
// the CIM export guide) carries a RequiredPerm, and a caller lacking it never
// sees the entry in the list AND is refused 403 on a direct fetch. General
// manuals carry no RequiredPerm -- any authenticated user may read them, which
// is why the mount is a plain RequireAuth rather than a single RBAC gate (see
// cmd/server/main.go): the gate that matters is inside, per document.
package docs

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Doc is one entry in the curated manual catalog. Filename is resolved
// against DOCS_DIR and is NEVER taken from the request -- the {id} path param
// only ever selects a Doc from the fixed manifest below. RequiredPerm empty
// means "any authenticated user"; a non-empty value is an RBAC permission key
// (auth.Perm*) the caller must hold to list or read the document.
type Doc struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Filename    string `json:"-"`
	// Format is how the viewer should render the content: "" or "markdown" is the
	// default (ReactMarkdown prose); "html" is a self-contained interactive guide
	// served verbatim and shown in a sandboxed iframe. The content endpoint returns
	// the file's bytes as a string either way -- only the client rendering differs.
	Format       string `json:"format,omitempty"`
	RequiredPerm string `json:"required_permission,omitempty"`
}

// manifest is the whole in-app library: the reference manuals, the how-to
// guides, the administrative references and the README front door
// (docs/README.md's Tier 1). Order here is the display order. The
// administrative documents (CIM export, symbology, admin/break-glass, and the
// logging & observability guide) carry a RequiredPerm; everything else is
// readable by any signed-in user. Filenames are the real docs/*.md names --
// verified present in the repo-root docs/ directory.
var manifest = []Doc{
	{
		ID:          "readme",
		Title:       "Start Here — Guide to the Guides",
		Description: "The documentation front door: what each manual covers and which to open for a given task.",
		Category:    "Start here",
		Filename:    "README.md",
	},
	{
		ID:          "user-handbook",
		Title:       "User Handbook",
		Description: "The everyday operator's manual: navigating the map, creating and editing features, connectivity, traces and search.",
		Category:    "Manuals",
		Filename:    "user-handbook.md",
	},
	{
		ID:          "engineering-studies",
		Title:       "Engineering Studies",
		Description: "The analysis deep-dive: load flow, fault current, arc flash and the rest of the powerflow engine, with the math behind each.",
		Category:    "Manuals",
		Filename:    "engineering-studies.md",
	},
	{
		ID:          "ev-charging-guide",
		Title:       "EV Charging & Feeder Headroom",
		Description: "Modelling a charging site end to end: the managed-charging load formula (connected load × diversity, clamped to the cap), how it loads the network, and the feeder-headroom study — what the number means and how to act on it.",
		Category:    "Manuals",
		Filename:    "ev-charging-guide.md",
	},
	{
		ID:          "substation-guide",
		Title:       "Substation Guide",
		Description: "Modelling and working with substations end to end — bays, panels, the site model and the live one-line.",
		Category:    "Manuals",
		Filename:    "substation-guide.md",
	},
	{
		ID:          "substation-guide-interactive",
		Title:       "Substation Guide (interactive)",
		Description: "The same substation model as a hands-on guide: hover to cross-reference the containment and connectivity graphs, click switches on a live one-line to watch the trace respond, and toggle fields to see what the load-flow engine makes of a node.",
		Category:    "Interactive guides",
		Filename:    "substation-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "ev-charging-guide-interactive",
		Title:       "EV Charging Guide (interactive)",
		Description: "Model a charging site end to end: a managed-charging calculator that shows the effective load the grid sees, a headroom-search visualiser you drag until a limit binds, and a planner that fits a site's cap to a feeder's headroom.",
		Category:    "Interactive guides",
		Filename:    "ev-charging-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "engineering-studies-interactive",
		Title:       "Engineering Studies (interactive)",
		Description: "The full analysis manual as a live workbench: one Feeder 7 schematic that every worked example hangs off, and calculators that run the real formulas and verify against the guide's own published numbers — the transformer model, fault level, a TCC coordination chart, IEEE-738 ampacity, the IEEE-1584 arc-flash model, the ramp-then-bisect hosting search, and more. The interactive companion to the Engineering Studies manual.",
		Category:    "Interactive guides",
		Filename:    "engineering-studies-interactive.html",
		Format:      "html",
	},
	{
		ID:          "network-sectioning-guide-interactive",
		Title:       "Feeders, Sections & Subnetworks (interactive)",
		Description: "One small Feeder 7 schematic you can operate: carve it into feeders, protection zones and switching sections, re-classify a device class and watch the sections re-draw, assign a circuit and see exactly what the stale badge can and can't catch, run a trace and read the section column, and plan an outage.",
		Category:    "Interactive guides",
		Filename:    "network-sectioning-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "lv-and-metering-guide-interactive",
		Title:       "LV Network & Metering (interactive)",
		Description: "Build LV supply chains against the real rule set (and see exactly why a hop is refused), size a cable, commission a pillar way by way, read a meter through a CT/VT bench, run the consumption roll-up and watch how double-counting happens, split a ring's load by resistance, and propagate private ownership downstream.",
		Category:    "Interactive guides",
		Filename:    "lv-and-metering-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "field-tool-guide-interactive",
		Title:       "Field Tool (interactive)",
		Description: "A working phone inside the guide: select, edit, move, create, delete, connect and relate features on a sample pack, scan tags, switch signal off, then run the real sync lifecycle — captured, applied, rejected, conflict, superseded — with office scenarios you control.",
		Category:    "Interactive guides",
		Filename:    "field-tool-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "user-handbook-interactive",
		Title:       "User Handbook (interactive)",
		Description: "The whole handbook as something you operate. A persona switch (viewer, analyst, editor, supervisor, admin) re-gates every widget by the real permission; plus a permission explorer built from the authorization seed data, territory grants, a working Query Builder, create/split/subtype/conflict editors, a trace and analysis bench, an operations storm-to-repair simulator, diagrams, annotations, field packs and sync review, the assistant, asset-tag minting with the real check character, and label printing.",
		Category:    "Interactive guides",
		Filename:    "user-handbook-interactive.html",
		Format:      "html",
	},
	{
		ID:          "land-spatial-guide-interactive",
		Title:       "Land & Spatial Management (interactive)",
		Description: "The land/spatial engine as something you operate on a small estate: run the four spatial modes (overlapping / inside / covering / nearby) with real point-in-polygon and geodesic distance; scan a conductor or a whole feeder for vegetation breaching its per-voltage clearance and raise a trim order onto a WAM board; and slide 'today' forward to watch easement-expiry and trim-due badges flip. The interactive companion to the Land & Spatial Management manual.",
		Category:    "Interactive guides",
		Filename:    "land-spatial-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "oms-guide-interactive",
		Title:       "OMS Outage Management — Interactive Guide",
		Description: "Run the real OMS rules in your browser on a Feeder 7 estate: trouble calls, prediction, who is affected versus dark, the live cone, switching orders that flip device state, a FLISR back-feed restoration suggester (which normally-open tie to close, load-flow-checked), planned outages, SAIDI/SAIFI, webhooks and an endpoints tester, with the manual's code-verified corrections.",
		Category:    "Interactive guides",
		Filename:    "oms-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "faults-guide-interactive",
		Title:       "Faults — Interactive Guide",
		Description: "Log an electrical fault, drive it through its lifecycle, link it to an outage and a repair, and see exactly what the API accepts, rejects and emits — a working in-browser copy of the Faults module.",
		Category:    "Interactive guides",
		Filename:    "faults-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "operations-guide-interactive",
		Title:       "Device Operations — Interactive Guide",
		Description: "Flip the operations log between its three reports (Timeline, trip-counts, device inventory), log an operation, watch it auto-fill from executed switching and booked faults, and deliver a SCADA event that resolves by external id and dedupes on re-send — a working in-browser copy of the Device Operations module.",
		Category:    "Interactive guides",
		Filename:    "operations-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "switching-and-protection-guide-interactive",
		Title:       "Switching & Protection (interactive)",
		Description: "Open, close and tag out devices on one Feeder 7 graph and watch the real trace rules: barriers, the de-energised cone, isolate, switching sections vs protection zones, the layer guard. Plot every TCC family and grade the feeder with CTI margins, step a recloser, fuse and sectionalizer sequence, cross-check fault duty and arc-flash, and run trip, lockout, switching order and restoration through a tie. The interactive companion to the Switching & Protection manual.",
		Category:    "Interactive guides",
		Filename:    "switching-and-protection-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "access-control-guide-interactive",
		Title:       "Access Control (interactive)",
		Description: "Run the access rules in your browser: a role designer, a request tester that follows the real middleware order, territory resolution and point-in-polygon assignment, a lockout and rate-limit lab, an SSO decision simulator, a break-glass drill, an audit query builder and a 'why can't Priya do X' diagnoser, with a persona switch over the real seeded roles.",
		Category:    "Interactive guides",
		Filename:    "access-control-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "feature-catalog-interactive",
		Title:       "Feature Catalog explorer (interactive)",
		Description: "A searchable explorer over the real feature-class model (63 classes, 372 fields, 53 domains, 103 connection rules, 87 relationship classes), built from the metadata seeds and checked against the manual: global search, the full manual reader with its appendices, a class browser and comparison, a 'which class do I use?' wizard, domain lookup, a create-form simulator and CSV header checker that mirror the real validation, a connectivity and relationship explorer, a feeder builder with a trace walker, the map-versus-diagram placement matrix, the axes, a compile preview and a manual-versus-seed drift dashboard. A persona switch uses the real RBAC roles.",
		Category:    "Interactive guides",
		Filename:    "feature-catalog-interactive.html",
		Format:      "html",
	},
	{
		ID:          "data-dictionary-interactive",
		Title:       "Data Dictionary explorer (interactive)",
		Description: "The data dictionary as a working explorer over the real model (63 classes, 372 fields, 53 domains, 96 distinct connection rules): global search, the role matrix, a compiled-table preview, where a missing value is caught (API versus database), operational-state transitions with a trace-barrier simulation, a domain browser with reverse lookup, class browser and comparison, a 'which class?' wizard, a connection and terminal explorer, the placement matrix, a create-form simulator with the real validation order and messages, a CSV header and value checker, and a manual-versus-seed drift dashboard. A persona switch uses the real roles.",
		Category:    "Interactive guides",
		Filename:    "data-dictionary-interactive.html",
		Format:      "html",
	},
	{
		ID:          "deployment-and-scaling-guide-interactive",
		Title:       "Production Deployment & Scaling (interactive)",
		Description: "Scaling the stateless API tier as a working model: locate where state lives, follow a request through the tiers, and size the login wave, probes, HPA and PDB, PgBouncer and connection budgets (with an Erlang-C queue), read-replica routing and lag, tile capacity, the worker queues and shared-pack builds. Then score the pre-flight checklist, plan a migration, set alerts and ask the scaling playbook what to turn when load rises. Includes a reality check of what is implemented versus recommended. A persona switch gates operator actions.",
		Category:    "Interactive guides",
		Filename:    "deployment-and-scaling-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "production-setup-runbook-interactive",
		Title:       "Production Setup Runbook (interactive)",
		Description: "The single-host Docker Compose install as a working rehearsal: explore every service and published port, build and check the .env against what the compose file actually passes through, reorder or skip the steps and see what breaks, run the migrations and seed phases with failure injection, do the first login and the login-flood and TLS checks, model a rolling upgrade, plan backup and restore, and diagnose symptoms. A persona switch gates operator actions.",
		Category:    "Interactive guides",
		Filename:    "production-setup-runbook-interactive.html",
		Format:      "html",
	},
	{
		ID:          "kubernetes-and-coolify-guide-interactive",
		Title:       "Kubernetes & Coolify Deployment (interactive)",
		Description: "Moving the stack onto a cluster or Coolify as a working model: translate between Compose, Coolify and Kubernetes, audit which env vars really reach a container, wire secrets and config, budget connections, build and lint a workload, tune probes, HPA and queue-depth scaling, simulate rolling updates, sequence the bootstrap and walk through incidents. A chooser compares Coolify and Kubernetes. A persona switch gates operator actions.",
		Category:    "Interactive guides",
		Filename:    "kubernetes-and-coolify-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "logging-and-observability-guide-interactive",
		Title:       "Logging & Observability (interactive)",
		Description: "The log pipeline as a working lab: trace the pipeline from app through Vector and Loki to Grafana, try the logger init and level thresholds, simulate the Admin logging screen, build request lines, check redaction and client-address spoofing, test the token gate, query a synthetic estate in LogQL, size retention and cardinality, replay alert rules and drill an incident. A persona switch gates operator actions.",
		Category:    "Interactive guides",
		Filename:    "logging-and-observability-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "asset-tags-guide-interactive",
		Title:       "Asset Tags (interactive)",
		Description: "Asset tagging as a working estate in your browser: the tag lifecycle and role permissions, a code generator with a real check-character lab, the one-primary index and registry trigger, the Tags tab and its exact errors, bulk legacy import, a label designer with real QR and Code 128, bulk print, a scan-and-resolve gauntlet with territory and draft scope, offline field-pack scanning, the admin console, the query tag filter, the assistant tools and a troubleshooting diagnoser. A persona switch applies the real role permissions.",
		Category:    "Interactive guides",
		Filename:    "asset-tags-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "cim-export-guide-interactive",
		Title:       "CIM/XML Export (interactive)",
		Description: "A working copy of the real CIM encoder (a JavaScript port that reproduces the server's Oakwood output byte-for-byte): follow a feature into its CIM objects, compute the UUIDv5 ids and see which ones are unstable, edit the declarative mapping and watch the file change, try the seven transforms, build connectivity nodes and the nearest-end heuristic, size an export job and its state machine, explore the file, classify classes into the four coverage buckets, build a hand-off note from the eighteen known gaps, see node-breaker vs bus-branch processing, generate the adapter pipeline for a recipient, reconcile counts, run a validator with defect injection, and generate the mapping SQL recipes. A persona switch gates export (features:read) and mapping edits (metadata:read/write).",
		Category:    "Interactive guides",
		Filename:    "cim-export-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "bill-of-quantities-guide-interactive",
		Title:       "Bill of Quantities & Cost Catalog (interactive)",
		Description: "The estimator and the price book as a working engine over a sample Feeder 7: a never-hidden Unpriced bucket, the real scope panel with currency switching and CSV/PDF previews, the rate-resolution ladder (override, material cost, subtype rate, class default), geodesic lengths, the wrong-unit-basis trap that silently ignores a rate, rounding, and the admin screens for currencies, the unit-rate price book and per-feature overrides - plus a POST /boq request builder and an Unpriced diagnoser. A persona switch applies territory visibility, private drafts and the metadata:write gate.",
		Category:    "Interactive guides",
		Filename:    "bill-of-quantities-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "field-tool-admin-guide-interactive",
		Title:       "Field Tool Admin (interactive)",
		Description: "The field back-office as a working simulation: follow any action through cmd/fieldsync and cmd/server, watch pack contents freeze from a crew's permissions, run the Field Packs page, size a shift-start build surge and the apply side (with PgBouncer and CPU limits), and drive the real review pipeline - policy, per-device lanes, head-of-line blocking, conflicts and supersession. Then define, grid, gate, schedule, reap and audit shared cells, stage a pack on behalf of a crew member, and diagnose faults against the live simulation. A persona switch gates every control by admin:field-packs, sync:review and sync:post.",
		Category:    "Interactive guides",
		Filename:    "field-tool-admin-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "symbology-guide-interactive",
		Title:       "Symbology & Map Styling (interactive)",
		Description: "The symbology manual as a working engine painting a sample Oakwood network: a why-can't-Priya-see-it pipeline with the cache and hard-refresh, a most-specific-wins scorer, a real SVG icon library and editor with live width/height and intrinsic-size diagnostics, a sizing calculator, the per-class rule editor (default rule, subtype and field targeting, line and polygon styles) with a click inspector, zoom bands vs Display zoom vs labels, map and diagram contexts side by side, and a seven-step diagnoser with SQL peeks. A persona switch gates every control by metadata permission.",
		Category:    "Interactive guides",
		Filename:    "symbology-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "admin-break-glass-guide-interactive",
		Title:       "Administration & Break-Glass (interactive)",
		Description: "The administrator's reference as a simulated deployment you can't hurt: send any caller through RequirePermission and the territory predicate, build the four column-by-role accounts and try each emergency, bring up a system in the wrong order, run the user console and sign-in lab (lockout, SSO linking, bearer tokens), resolve territory scope from the two trees, work the SSO-outage runbook, plan and run resetdata and uncompile, evolve a class with blueprint-vs-building, and query the audit trail. A persona switch changes who is acting.",
		Category:    "Interactive guides",
		Filename:    "admin-break-glass-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "everyday-operations-interactive",
		Title:       "Everyday Operations (interactive)",
		Description: "The task cookbook as a live, simulated Oakwood map: pan/zoom/box-zoom with layers and a full status bar, a Features browser and Query builder (filter, aggregate, radius scope), create-and-duplicate workbench, attribute editing with the real PATCH body and 409 guard, move/reshape/split/delete geometry tools, measure with live geodesic labels and units, a Snap/Ortho playground, connect-and-trace with loop check and network rules, relationships with the Kind picker, guided assemblies, redlines, labels, files and links, a settings lab that shows what survives a reload, and a missing-button diagnoser. A persona switch re-gates every widget by permission and territory.",
		Category:    "Interactive guides",
		Filename:    "everyday-operations-interactive.html",
		Format:      "html",
	},
	{
		ID:          "data-import-guide-interactive",
		Title:       "Data Import (interactive)",
		Description: "The import engine as something you operate: a persona switch gating each action by metadata:write (author) vs features:write (run), a source chooser across all nine formats, a profile builder, a live transform playground (direct/constant/scale/domain_map with the real blank-and-error behaviour), a dry-run simulator that validates editable sample rows row-by-row and writes nothing, a write-mode/upsert decision widget, a permission matrix that follows the persona, and a quiz.",
		Category:    "Interactive guides",
		Filename:    "data-import-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "external-service-layers-guide-interactive",
		Title:       "External Service Layers (interactive)",
		Description: "Overlaying third-party map services as something you operate: a persona switch gating registration by metadata:write, a service-type chooser across all seven protocols (WMS/WMTS/XYZ/vector-tile/Esri MapServer/Esri FeatureServer/WFS), a registration form showing the write-only sealed credential and the forced-proxy rule, a mock Layers-panel with toggles and opacity, a per-type identify demo (server GetFeatureInfo/identify vs client hit-test), a live SSRF-gate checker that blocks private/loopback/CGNAT/NAT64, and a quiz.",
		Category:    "Interactive guides",
		Filename:    "external-service-layers-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "integrations-guide-interactive",
		Title:       "Integrations & External Systems (interactive)",
		Description: "The external-system surface as something you operate, end to end: a persona switch gating the catalog & webhooks (metadata:write) vs feature links & references (features:write); a system-registration form showing the sealed AES-256-GCM credential; a link-kind validator enforcing the relative-path SSRF rule; a live external_ref editor (scalar & object refs) with its reverse index; a path-template URL builder that fills {id}/{field} and blocks a non-relative result; a real in-browser HMAC-SHA256 signer with auth modes, the 5-minute replay window and idempotency; a faithful mapping-spec playground (items_path + const/path/fields + map/default, strict-decode errors); an adopt-not-duplicate correlation simulator; pull & outbound direction configs; the agent/MCP tool surface; a security chapter with an SSRF path checker; and a ten-question quiz.",
		Category:    "Interactive guides",
		Filename:    "integrations-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "validation-and-audit-guide-interactive",
		Title:       "Data Validation & Audit (interactive)",
		Description: "The continuous-validation system as something you operate: a scale calculator that shows the batch pile growing while the live queue stays flat, the three lines of defence, the finding kinds and the scope trick, a self-pruning queue you drive (introduce a defect, reconcile, fix it, watch it auto-resolve — idempotent by fingerprint), a feature_history tail with a cursor and the truncation guard, a gated triage queue, and the admin:users activity audit with private-drafts respected. A persona switch gates reconcile/triage (connectivity:commit) and the audit (admin:users).",
		Category:    "Interactive guides",
		Filename:    "validation-and-audit-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "works-inspections-maintenance-guide-interactive",
		Title:       "Works, Inspections & Maintenance (interactive)",
		Description: "The WAM manual as something you operate: a persona switch gating every widget by wam:write, a live work board, the defect and work-order state machines with the real auto-status and severity-to-priority rules, a template builder and retire/restore library, an inspection recorder that auto-raises defects and replays idempotently, a maintenance-cycle simulator, Femi's offline field round with outbox sync, the permission and territory gaps, an assistant triage run over the wam_* tools, and a quiz.",
		Category:    "Interactive guides",
		Filename:    "works-inspections-maintenance-guide-interactive.html",
		Format:      "html",
	},
	{
		ID:          "works-inspections-maintenance-guide",
		Title:       "Works, Inspections & Maintenance",
		Description: "The Work & Asset Management manual: defects, inspections and the typed work-order lifecycle, on the desk and in the field.",
		Category:    "Manuals",
		Filename:    "works-inspections-maintenance-guide.md",
	},
	{
		ID:          "field-tool-guide",
		Title:       "Field Tool Guide",
		Description: "The field crew's manual for the offline mobile app: downloading a work-area pack, viewing and editing the network offline (attributes, geometry, create, delete, connect), GPS/photo capture, inspections and faults, and syncing back — with the colour code and sync lifecycle explained.",
		Category:    "Manuals",
		Filename:    "field-tool-guide.md",
	},
	{
		ID:          "feature-catalog",
		Title:       "Feature Catalog",
		Description: "The per-class reference to every feature type in the model, organised by electrical layer.",
		Category:    "Manuals",
		Filename:    "feature-catalog.md",
	},
	{
		ID:          "data-dictionary",
		Title:       "Data Dictionary",
		Description: "A short definition of every feature class and every field — plus the system columns and shared coded domains — for anyone entering or importing data.",
		Category:    "Manuals",
		Filename:    "data-dictionary.md",
	},
	{
		ID:          "lv-and-metering-guide",
		Title:       "LV Network & Metering",
		Description: "Modelling the low-voltage network and metering end to end — conductors, pillars, service points, revenue/MV/network meters, and the consumption rollup, with worked examples.",
		Category:    "Manuals",
		Filename:    "lv-and-metering-guide.md",
	},
	{
		ID:          "network-sectioning-guide",
		Title:       "Feeders, Sections & Subnetworks",
		Description: "VoltEdge's lean take on subnetworks — feeders (circuits), protection zones and switching sections, the boundary_role backbone, section highlighting, and feeder freshness.",
		Category:    "Manuals",
		Filename:    "network-sectioning-guide.md",
	},
	{
		ID:          "switching-and-protection-guide",
		Title:       "Switching & Protection",
		Description: "The switching and protection devices end to end: switch/breaker/isolator/recloser/sectionalizer/fuse and the RMU, the device_state vs operational_state axes, how opening a device re-routes traces and de-energises a cone, ties/couplers, and the relay → curve → coordination chain with CTI grading.",
		Category:    "Manuals",
		Filename:    "switching-and-protection-guide.md",
	},
	{
		ID:          "faults-guide",
		Title:       "Faults — Logging & Managing Fault Events",
		Description: "The fault/outage RECORD module (distinct from the fault-current engine): the fault_events model, the detected → investigating → cleared → closed lifecycle and its audit history, how to log a fault, and the four links that make a fault the hinge between an OMS outage (it explains) and a WAM defect (it books).",
		Category:    "Manuals",
		Filename:    "faults-guide.md",
	},
	{
		ID:          "oms-guide",
		Title:       "OMS — Outage Management",
		Description: "The outage lifecycle end to end: trouble calls rolled into predicted incidents (blamed on the nearest protective device), affected-customer and critical-facility rollups, the live de-energised cone, switching orders whose open/close steps flip the real device_state, planned outages, restoration and the SAIDI/SAIFI/CAIDI/ENS reliability indices.",
		Category:    "Manuals",
		Filename:    "oms-guide.md",
	},
	{
		ID:          "flisr-restoration-guide",
		Title:       "FLISR — Service Restoration by Back-Feed",
		Description: "Restoring a dead section by back-feed: how VoltEdge suggests which normally-open tie to close to re-energise an outaged section from a neighbouring feeder, load-flow-checks whether that feeder can carry it (superseding the nameplate estimate), ranks the plans, and drafts the one-click close-tie switching order.",
		Category:    "Manuals",
		Filename:    "flisr-restoration-guide.md",
	},
	{
		ID:          "operations-guide",
		Title:       "Device Operations — Logging & Reporting Device Trips",
		Description: "The device-operation log: recording and reporting every protective/switching device operation (trip, lockout, reclose, open, close). The three reports (Timeline, By device trip-counts, Devices inventory with current state), how the log auto-fills from switching, faults, crews and SCADA/webhooks, the external-ref device correlation, and how it links to Outage Management and Faults.",
		Category:    "Manuals",
		Filename:    "operations-guide.md",
	},
	{
		ID:          "safety-guide",
		Title:       "Operational Safety — Tags, Grounds & Clearances",
		Description: "The safety-document layer and its interlock: danger/out-of-service tags that BAR a device from being operated, applied grounds, and clearances (permits-to-work) that bind them and hand a work area to a crew. How a switching step is refused on a tagged device, how to issue a clearance from a switching order (auto-tagging the devices it opens), and how surrendering it releases everything.",
		Category:    "Manuals",
		Filename:    "safety-guide.md",
	},
	{
		ID:          "land-spatial-guide",
		Title:       "Land & Spatial Management",
		Description: "The land the network crosses and the geometry-derived engine over it: the easement/parcel/corridor/vegetation classes; the spatial query (what's inside / covering / near a feature, from the shapes); vegetation clearance (trees within the clearance a conductor's voltage requires) and the one-click WAM trim order; and the due tracking for expiring easements and overdue trims.",
		Category:    "Manuals",
		Filename:    "land-spatial-guide.md",
	},
	{
		ID:          "outage-operations-guide",
		Title:       "Outage Operations — end to end",
		Description: "The connective overview: one Feeder 7 outage run all the way through trouble call → fault → incident → switching → restore → defect → work order → reliability, showing how the Faults, OMS and WAM modules link, who owns each leg, the variations (minor fault, planned, storm, inspection-found), and what's automated vs. driven by hand.",
		Category:    "Manuals",
		Filename:    "outage-operations-guide.md",
	},
	{
		ID:          "reliability-indices-guide",
		Title:       "Reliability & Indices",
		Description: "The deep-dive behind SAIDI/SAIFI/CAIDI/ENS: the exact formulas as coded, the per-system-customer denominator derived from live service points, how interruptions are clipped to the window, the unplanned-only filter, the by-fault-cause breakdown, and the honest limits — no IEEE 1366 major-event exclusion, no MAIFI, ENS a flat-kW estimate.",
		Category:    "Manuals",
		Filename:    "reliability-indices-guide.md",
	},
	{
		ID:          "analysis-validation-report",
		Title:       "Analysis Validation Report",
		Description: "The test-backed evidence behind the analysis numbers: what is validated against an external reference (load-flow vs exact AC, fault vs IEC 60909, the voltage factor c, coordination verdict) to what tolerance and by which Go test, and an honest list of what is NOT yet validated (unbalanced vs IEEE feeders, end-to-end-from-DB paths, meshed/nodal, scale, arc-flash).",
		Category:    "Manuals",
		Filename:    "analysis-validation-report.md",
	},
	{
		ID:          "asset-tags-guide",
		Title:       "Asset Tags Guide",
		Description: "How to use asset tags and how they work: minting, affixing, scanning to resolve, printing and querying by tag.",
		Category:    "How-to guides",
		Filename:    "asset-tags-guide.md",
	},
	{
		ID:          "everyday-operations",
		Title:       "Everyday Operations — Task Cookbook",
		Description: "The hands-on companion to the handbook: one recipe per task — create and edit features, measure, snap, connect, annotate.",
		Category:    "How-to guides",
		Filename:    "everyday-operations.md",
	},
	{
		// Readable by any signed-in user: the operator half (running an import)
		// needs features:write, the admin half (authoring profiles + mappings) is
		// marked metadata:write inside the guide, so one document serves both the
		// mapping author and the operator who runs the load.
		ID:          "data-import-guide",
		Title:       "Data Import Guide",
		Description: "Bringing data in from other systems: authoring import profiles, the supported sources (CSV, GeoJSON, shapefile, KML, DXF, GeoPackage, SpatiaLite, live PostGIS/MSSQL), field mapping, dry-run and the load.",
		Category:    "How-to guides",
		Filename:    "data-import-guide.md",
	},
	{
		ID:          "external-service-layers-guide",
		Title:       "External Service Layers",
		Description: "Overlaying third-party map services on the map (ArcGIS FeatureServer/MapServer, WMS, WMTS, WFS, XYZ, vector tiles): registering the Service-layers catalogue, auth and the sealed credential, the Discover auto-fill, the SSRF-hardened proxy, toggling layers on the map, and click-to-identify.",
		Category:    "How-to guides",
		Filename:    "external-service-layers-guide.md",
	},
	{
		// Dual-audience (like Bill of Quantities): operators link/tag features
		// (features:write) while administrators register systems and webhooks
		// (metadata:write); both are covered, admin sections marked inline, so no
		// single RequiredPerm fits -- readable by any signed-in user.
		ID:          "integrations-guide",
		Title:       "Integrations & External Systems",
		Description: "Connecting VoltEdge to SCADA, AMI, CIS and other systems: registering the external-systems catalog, linking a feature outward, tagging a feature's id in another system (external_ref) and pulling live data, and receiving inbound pushes via webhooks (HMAC, correlation and adopt-not-duplicate).",
		Category:    "How-to guides",
		Filename:    "integrations-guide.md",
	},
	{
		// Readable by any signed-in user: the issue queue is a connectivity
		// capability (connectivity:read to view, connectivity:commit to triage) and
		// the activity audit is admin:users -- both gates are marked inside the
		// guide, so one document serves the data steward, the reviewer and the
		// auditor.
		ID:          "validation-and-audit-guide",
		Title:       "Data Validation & Audit",
		Description: "Keeping a high-change network trustworthy: continuous incremental validation (the worker that tails feature_history), the self-pruning issue queue (fingerprints, auto-resolve, the truncation guard), triaging the queue, and the activity audit — who changed what, when.",
		Category:    "How-to guides",
		Filename:    "validation-and-audit-guide.md",
	},
	{
		// Readable by any signed-in user: running the BoQ tool needs only
		// features:read. The administrative half (currencies, rates, overrides) is
		// marked metadata:write inside the manual, so one document serves both the
		// estimator and the catalog administrator.
		ID:          "bill-of-quantities-guide",
		Title:       "Bill of Quantities & Cost Catalog",
		Description: "Pricing any slice of the network as a costed schedule, and administering the price book behind it — currencies, unit rates and per-feature overrides.",
		Category:    "How-to guides",
		Filename:    "bill-of-quantities-guide.md",
	},
	{
		// Administrative: the CIM export mapping tables are metadata configuration,
		// so this manual is gated on the same read capability the /admin metadata
		// screens use.
		ID:           "cim-export-guide",
		Title:        "CIM Export Guide",
		Description:  "Configuring and running the CIM/XML export: the mapping tables, the reference dictionary and per-class preview.",
		Category:     "Administration",
		Filename:     "cim-export-guide.md",
		RequiredPerm: auth.PermMetadataRead,
	},
	{
		// Administrative: symbology and map styling are metadata configuration,
		// authored on the /admin screens, so this manual is gated on the same
		// read capability the CIM export guide uses.
		ID:           "symbology-guide",
		Title:        "Symbology & Map Styling Guide",
		Description:  "Authoring the symbols features wear on the map: drawing SVG icons, attaching them to classes, and filtering by subtype and field value.",
		Category:     "Administration",
		Filename:     "symbology-guide.md",
		RequiredPerm: auth.PermMetadataRead,
	},
	{
		ID:           "access-control-guide",
		Title:        "Access Control Guide",
		Description:  "Users, roles, permissions, territories and the audit trail for administrators and auditors: the nine seeded roles, all 34 permissions, a route-to-permission map, sessions, SSO, tokens, break-glass, spatial scoping, private drafts, runbooks and an auditor's checklist.",
		Category:     "Administration",
		Filename:     "access-control-guide.md",
		RequiredPerm: auth.PermUsersManage,
	},
	{
		// Administrative: break-glass and access administration. Gated on the
		// user-administration capability -- the audience for this manual.
		ID:           "admin-break-glass-guide",
		Title:        "Administration & Break-Glass Guide",
		Description:  "Administering VoltEdge: users, roles and permissions, spatial access control, and the break-glass override.",
		Category:     "Administration",
		Filename:     "admin-break-glass-guide.md",
		RequiredPerm: auth.PermUsersManage,
	},
	{
		// Administrative/operations: running the offline field tool for a fleet --
		// pack management, the sync-review QA queue, and the build/sync scaling
		// knobs. Gated on the dedicated field-packs admin capability (the audience).
		ID:           "field-tool-admin-guide",
		Title:        "Field Tool Admin Guide",
		Description:  "Running the offline field tool: crew access and the RBAC that gates pack contents, managing everyone's packs (disable/delete), the sync-review QA queue (hold/post/reject/resolve conflicts), and tuning build/sync for a fleet (zoom, size cap, worker concurrency, replicas).",
		Category:     "Administration",
		Filename:     "field-tool-admin-guide.md",
		RequiredPerm: auth.PermFieldPacksAdmin,
	},
	{
		// Administrative/operations: configuring the structured logs, reading a
		// request's trail, and shipping logs off the box. Gated on the same
		// user-administration capability as the break-glass guide -- the "people who
		// run VoltEdge" audience.
		ID:           "logging-and-observability-guide",
		Title:        "Logging & Observability Guide",
		Description:  "What the services log and how: configuring format and level, correlating a request by its id, the PII policy, and shipping logs to an aggregator.",
		Category:     "Administration",
		Filename:     "logging-and-observability-guide.md",
		RequiredPerm: auth.PermUsersManage,
	},
	{
		// Administrative/operations: running VoltEdge at scale in production --
		// replicas, load balancing, Postgres read replicas, connection pooling, tile
		// and analysis scaling. Same "people who run VoltEdge" audience, so gated on
		// the user-administration capability like the logging and break-glass guides.
		ID:           "deployment-and-scaling-guide",
		Title:        "Production Deployment & Scaling",
		Description:  "Running VoltEdge in production at high concurrency: stateless API replicas, load balancing, Postgres read replicas and PgBouncer, tile and analysis scaling, Kubernetes shape, and the pre-flight checklist.",
		Category:     "Administration",
		Filename:     "deployment-and-scaling-guide.md",
		RequiredPerm: auth.PermUsersManage,
	},
	{
		// Administrative/operations: the step-by-step install for the production
		// docker-compose.prod.yml stack -- secrets, images, SPA build, migrations,
		// the first admin, TLS, backups, upgrades. Same "people who run VoltEdge"
		// audience, gated on user-administration like the scaling guide.
		ID:           "production-setup-runbook",
		Title:        "Production Setup Runbook",
		Description:  "Step-by-step install of the production stack: environment/secrets, building images and the SPA, migrating and seeding the database, the first break-glass admin, TLS, backups, upgrades and verification.",
		Category:     "Administration",
		Filename:     "production-setup-runbook.md",
		RequiredPerm: auth.PermUsersManage,
	},
	{
		// Administrative/operations: running the stack on the two realistic
		// platforms beyond single-host compose -- Coolify (PaaS over compose) and
		// Kubernetes (native objects + an operator). Same audience/gate.
		ID:           "kubernetes-and-coolify-guide",
		Title:        "Deploying on Kubernetes & Coolify",
		Description:  "Taking the production stack to Coolify (a PaaS wrapper over Docker Compose) or Kubernetes (native Deployments/HPA/Ingress + a Postgres operator), with the portable web image and the platform-specific gotchas.",
		Category:     "Administration",
		Filename:     "kubernetes-and-coolify-guide.md",
		RequiredPerm: auth.PermUsersManage,
	},
}

// docsDir is the directory the manual files are read from. DOCS_DIR overrides
// it; the default ../docs is correct when the server runs from api/ (its
// working directory under `task api-dev` and the compose service), reaching the
// repo-root docs/ sibling of the Go module.
func docsDir() string {
	if v := os.Getenv("DOCS_DIR"); v != "" {
		return v
	}
	return filepath.Join("..", "docs")
}

// canSee reports whether the caller may list or read d. A document with no
// RequiredPerm is visible to any authenticated user; otherwise the caller must
// hold the permission -- or be a break-glass admin, who overrides every
// permission (Has does not short-circuit on break-glass yet, so we check it
// explicitly here, the same belt-and-braces the rest of the app uses).
func canSee(user *auth.SessionUser, d Doc) bool {
	if d.RequiredPerm == "" {
		return true
	}
	return user.BreakGlass || user.Has(d.RequiredPerm)
}

// Routes returns the docs viewer endpoints, mounted at /docs behind a plain
// RequireAuth (see cmd/server/main.go) -- every signed-in user reaches the
// mount, and the per-document RequiredPerm below is what actually restricts the
// administrative manuals:
//
//	GET /docs        the catalog, filtered to the documents the caller may read
//	GET /docs/{id}   one document's Markdown (403 if it is restricted and the caller lacks the permission; 404 if unknown)
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", listHandler())
	r.Get("/{id}", contentHandler())
	return r
}

// listHandler returns only the documents the caller is allowed to read, so the
// viewer's index never advertises a manual the user would be refused. Each
// entry still reports its required_permission so the UI can badge the
// administrative ones.
func listHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		visible := make([]Doc, 0, len(manifest))
		for _, d := range manifest {
			if canSee(user, d) {
				visible = append(visible, d)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"docs": visible})
	}
}

// docContent is one document over the wire: its catalog metadata plus the raw
// Markdown the frontend renders. Kept separate from Doc so the list stays lean
// (no bodies) while a single fetch carries the content.
type docContent struct {
	Doc
	Content string `json:"content"`
}

// contentHandler serves one manual's Markdown. The {id} selects a manifest
// entry -- never a filesystem path -- so the read is confined to the curated
// set; an unknown id is a 404, a restricted document the caller can't read is a
// 403 (the server-enforced gate the feature was asked for), and a manifest
// filename that is missing on disk is a 500 with a clear message.
func contentHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		id := chi.URLParam(r, "id")
		var doc *Doc
		for i := range manifest {
			if manifest[i].ID == id {
				doc = &manifest[i]
				break
			}
		}
		if doc == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such document"})
			return
		}
		if !canSee(user, *doc) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "you do not have the required permission (" + doc.RequiredPerm + ")"})
			return
		}

		body, err := os.ReadFile(filepath.Join(docsDir(), doc.Filename))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "document file not found on server (check DOCS_DIR)"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, docContent{Doc: *doc, Content: string(body)})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
