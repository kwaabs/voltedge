package assettag

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/featureregistry"
	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// Routes returns the asset-tag endpoints:
//
//	GET  /tags/{code}            resolve a scanned code -> {tag, feature, tags} (404 if unknown/hidden/retired)
//	GET  /tags?code=<c>          resolve by query (for codes with URL-reserved characters / manual entry)
//	GET  /tags?feature_class_code=&feature_id=    every tag on a feature (the panel Tags tab)
//	GET  /tags?feature_class_code=&feature_ids=a,b,c   the one printable (primary) tag per feature (bulk label print)
//	POST /tags                   register a legacy/external code {feature_class_code, feature_id, tag_code, make_primary}
//	POST /tags/printed           batch bump print_count {codes:[...]} (a bulk sheet was sent to print)
//	POST /tags/{code}/primary    promote a tag to its feature's primary
//	POST /tags/{code}/void       retire a tag
//	POST /tags/{code}/reissue    mint a fresh code, supersede this one
//	POST /tags/{code}/printed    bump print_count (a label was sent to print)
//
// The GET verbs read (tags:read) and the mutating verbs write (tags:write); the
// method-aware gate RequirePermission(tags:read, tags:write) is applied on the
// mount in api/cmd/server/main.go, so this router carries no auth of its own --
// the same arrangement oms / wam / faults use. Minting is NOT an endpoint: every
// feature is auto-tagged by the DB trigger on runtime.feature_registry
// (migration 0114), never over HTTP.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()

	// Relative to the /tags mount (see cmd/server/main.go) -- the oms/wam/faults
	// convention; the mount adds the prefix.
	r.Get("/", rootGetHandler(db))
	r.Get("/{code}", resolveHandler(db))
	r.Post("/", registerHandler(db))
	r.Post("/printed", printedBatchHandler(db))
	r.Post("/{code}/primary", setPrimaryHandler(db))
	r.Post("/{code}/void", voidHandler(db))
	r.Post("/{code}/reissue", reissueHandler(db))
	r.Post("/{code}/printed", printedHandler(db))

	return r
}

// rootGetHandler serves the two query-string reads: resolve-by-query (a code with
// URL-reserved characters, or manual entry) and list-a-feature's-tags. Splitting
// them by which params are present keeps one clean mount for both.
func rootGetHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// The org-wide management console (Admin -> Asset Tags): view=list is the
		// filtered/paged browse, view=stats the KPI strip. Both are org-wide reads
		// (no feature id), scoped to the caller's territories/drafts through the
		// registry join, so they sit here on the read side of the mount's gate
		// (tags:read) beside the per-feature reads.
		switch strings.TrimSpace(q.Get("view")) {
		case "list":
			listTags(w, r, db, q)
			return
		case "stats":
			tagStats(w, r, db)
			return
		}
		if code := strings.TrimSpace(q.Get("code")); code != "" {
			resolve(w, r, db, code)
			return
		}
		class := strings.TrimSpace(q.Get("feature_class_code"))
		// Bulk primary lookup (the /features browser's "print selected tags"):
		// ?feature_class_code=&feature_ids=a,b,c returns the one printable tag per
		// feature. GET so it stays on the read side of the method-aware mount gate
		// (tags:read) -- printing a label is a read; the id list still fits a query
		// (the client chunks large selections).
		if idsRaw := strings.TrimSpace(q.Get("feature_ids")); idsRaw != "" {
			if class == "" {
				writeError(w, invalidf("feature_class_code is required with feature_ids"))
				return
			}
			tags, err := PrimaryForFeatures(r.Context(), db, class, strings.Split(idsRaw, ","))
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
			return
		}
		id := strings.TrimSpace(q.Get("feature_id"))
		if class == "" || id == "" {
			writeError(w, invalidf("provide either ?code= to resolve, ?feature_class_code=&feature_id= to list a feature's tags, or ?feature_class_code=&feature_ids=a,b,c for a bulk primary lookup"))
			return
		}
		// ListForFeature does no scope check itself (it trusts the caller to have
		// resolved the feature through a scoped read); this HTTP front door hasn't,
		// so gate here -- don't list tags for a soft-deleted / out-of-territory /
		// peer-private-draft / missing feature. EnforceFeatureReadScope answers
		// not-found for all of them, the same answer a feature read gives.
		loaded, err := metadata.Load(r.Context(), db, class)
		if err != nil {
			writeError(w, invalidf("unknown feature class %q", class))
			return
		}
		if err := crud.EnforceFeatureReadScope(r.Context(), db, loaded, id); err != nil {
			if crud.IsNotFoundError(err) {
				writeError(w, notFoundf("no feature %s in %q", id, class))
				return
			}
			writeError(w, err)
			return
		}
		tags, err := ListForFeature(r.Context(), db, class, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
	}
}

// listTags serves the console's browse/search: GET /tags?view=list with optional
// search / status / source / feature_class_code / primary_only filters and
// limit+offset paging. It resolves the caller's registry scope (territory +
// private-draft visibility) and passes it to List so the page shows only tags the
// caller could resolve one at a time.
func listTags(w http.ResponseWriter, r *http.Request, db *bun.DB, q url.Values) {
	scope, err := featureregistry.ResolveScope(r.Context(), db)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := List(r.Context(), db, ListFilter{
		Search:      q.Get("search"),
		Status:      q.Get("status"),
		Source:      q.Get("source"),
		Class:       q.Get("feature_class_code"),
		PrimaryOnly: q.Get("primary_only") == "true",
		Limit:       atoiOr(q.Get("limit"), 0),
		Offset:      atoiOr(q.Get("offset"), 0),
	}, scope)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// tagStats serves the console's KPI strip: GET /tags?view=stats, scoped like
// listTags so the counts match what the caller can browse.
func tagStats(w http.ResponseWriter, r *http.Request, db *bun.DB) {
	scope, err := featureregistry.ResolveScope(r.Context(), db)
	if err != nil {
		writeError(w, err)
		return
	}
	stats, err := Stats(r.Context(), db, scope)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// atoiOr parses a query-string integer, falling back to def on empty/garbage --
// the store clamps the range, so a nonsense limit just becomes the default.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

// printedBatchHandler bumps print_count for a whole sheet of codes at once -- the
// batch twin of printedHandler, fired when the /features browser sends a bulk
// label sheet to print. Best-effort book-keeping: unknown codes are skipped and
// it reports how many rows it bumped.
func printedBatchHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Codes []string `json:"codes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, invalidf("invalid JSON body: %v", err))
			return
		}
		n, err := MarkManyPrinted(r.Context(), db, in.Codes)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"updated": n})
	}
}

func resolveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resolve(w, r, db, chi.URLParam(r, "code"))
	}
}

// resolve is the shared body for the path and query resolve routes.
func resolve(w http.ResponseWriter, r *http.Request, db *bun.DB, code string) {
	res, err := Resolve(r.Context(), db, code)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func registerHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in RegisterInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, invalidf("invalid JSON body: %v", err))
			return
		}
		tag, err := Register(r.Context(), db, in, auth.ActorID(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, tag)
	}
}

func setPrimaryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag, err := SetPrimary(r.Context(), db, chi.URLParam(r, "code"), auth.ActorID(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tag)
	}
}

func voidHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag, err := Void(r.Context(), db, chi.URLParam(r, "code"), auth.ActorID(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tag)
	}
}

func reissueHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag, err := Reissue(r.Context(), db, chi.URLParam(r, "code"), auth.ActorID(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, tag)
	}
}

func printedHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag, err := MarkPrinted(r.Context(), db, chi.URLParam(r, "code"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tag)
	}
}
