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
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	Filename     string `json:"-"`
	RequiredPerm string `json:"required_permission,omitempty"`
}

// manifest is the whole in-app library: the six manuals plus the two how-to
// guides and the README front door (docs/README.md's Tier 1). Order here is
// the display order. The two administrative documents carry a RequiredPerm;
// everything else is readable by any signed-in user. Filenames are the real
// docs/*.md names -- verified present in the repo-root docs/ directory.
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
		ID:          "substation-guide",
		Title:       "Substation Guide",
		Description: "Modelling and working with substations end to end — bays, panels, the site model and the live one-line.",
		Category:    "Manuals",
		Filename:    "substation-guide.md",
	},
	{
		ID:          "works-inspections-maintenance-guide",
		Title:       "Works, Inspections & Maintenance",
		Description: "The Work & Asset Management manual: defects, inspections and the typed work-order lifecycle, on the desk and in the field.",
		Category:    "Manuals",
		Filename:    "works-inspections-maintenance-guide.md",
	},
	{
		ID:          "feature-catalog",
		Title:       "Feature Catalog",
		Description: "The per-class reference to every feature type in the model, organised by electrical layer.",
		Category:    "Manuals",
		Filename:    "feature-catalog.md",
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
