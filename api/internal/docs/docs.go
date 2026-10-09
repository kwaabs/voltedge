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
