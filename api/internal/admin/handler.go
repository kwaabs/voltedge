// Package admin is the first real write path into metadata.* -- every
// feature class, subtype, field, domain, and field group in this project
// has been authored by hand so far (seed/*.sql, migrations/*.sql), never
// through the API. See docs/phase-6-admin-metadata.md for the full
// design and why this slice deliberately stops short of editing an
// already-compiled class's subtypes or an already-referenced domain's
// values (the schema compiler doesn't safely support altering either
// yet -- see api/internal/compiler/compiler.go's own existing-table
// path, which only ever ADD COLUMNs, never touches a CHECK constraint
// baked in at initial CREATE TABLE).
package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/compiler"
	"github.com/uptrace/bun"
)

// Routes returns a router of the metadata-authoring endpoints, meant to
// be mounted at /admin:
//
//	GET    /feature-classes                every class, any status, with subtype/field counts
//	POST   /feature-classes                create a brand-new class end to end (subtypes, fields, terminals, one default symbology rule)
//	PATCH  /feature-classes/{code}         edit name/is_diagram_container (any class), or geometry_type/srid/network_role (draft only)
//	DELETE /feature-classes/{code}         delete a draft (never-compiled) class
//	POST   /feature-classes/{code}/compile compile/recompile -- wraps api/internal/compiler.Compile directly
//	GET    /domains                        every domain with its values
//	POST   /domains                        create a domain, optionally with initial coded values
//	DELETE /domains/{code}                 delete a domain not referenced by any field
//	GET    /field-groups                   every field group
//	POST   /field-groups                   create a field group
//	DELETE /field-groups/{code}            delete a field group not referenced by any field
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/feature-classes", listFeatureClassesHandler(db))
	r.Post("/feature-classes", createFeatureClassHandler(db))
	r.Patch("/feature-classes/{code}", updateFeatureClassHandler(db))
	r.Delete("/feature-classes/{code}", deleteFeatureClassHandler(db))
	r.Post("/feature-classes/{code}/compile", compileFeatureClassHandler(db))
	r.Get("/domains", listDomainsHandler(db))
	r.Post("/domains", createDomainHandler(db))
	r.Delete("/domains/{code}", deleteDomainHandler(db))
	r.Get("/field-groups", listFieldGroupsHandler(db))
	r.Post("/field-groups", createFieldGroupHandler(db))
	r.Delete("/field-groups/{code}", deleteFieldGroupHandler(db))
	return r
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case isValidationError(err):
		status = http.StatusBadRequest
	case isNotFoundError(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, invalid("request body is not valid JSON: %v", err))
		return false
	}
	return true
}

func listFeatureClassesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		classes, err := listFeatureClasses(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"feature_classes": classes})
	}
}

func createFeatureClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createFeatureClassRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createFeatureClass(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateFeatureClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req updateFeatureClassRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateFeatureClass(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteFeatureClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if err := deleteFeatureClass(r.Context(), db, code); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteDomainHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if err := deleteDomain(r.Context(), db, code); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteFieldGroupHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if err := deleteFieldGroup(r.Context(), db, code); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// compileFeatureClassHandler wraps compiler.Compile directly -- no
// admin-specific compile logic exists or should exist; this is the exact
// same function cmd/compile's CLI has called since Phase 1, just now
// reachable over HTTP so the wizard (or a later "recompile" action on an
// existing class) doesn't need a terminal.
func compileFeatureClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		result, err := compiler.Compile(r.Context(), db, code)
		if err != nil {
			writeError(w, invalid("compile %s: %v", code, err))
			return
		}
		writeJSON(w, http.StatusOK, compileResponse{
			FeatureClassCode:  result.FeatureClassCode,
			PhysicalTable:     result.PhysicalTable,
			Created:           result.Created,
			ColumnsAdded:      result.ColumnsAdded,
			MetadataVersionID: result.MetadataVersionID,
		})
	}
}

func listDomainsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domains, err := listDomains(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"domains": domains})
	}
}

func createDomainHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createDomainRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createDomain(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func listFieldGroupsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groups, err := listFieldGroups(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"field_groups": groups})
	}
}

func createFieldGroupHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createFieldGroupRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createFieldGroup(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}
