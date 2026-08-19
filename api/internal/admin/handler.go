// Package admin is the first real write path into metadata.* -- every
// feature class, subtype, field, domain, and field group in this project
// has been authored by hand so far (seed/*.sql, migrations/*.sql), never
// through the API. See docs/phase-6-admin-metadata.md for the full
// design. Adding a subtype to an already-compiled class, a value to an
// already-referenced coded domain, or a new field to an already-existing
// class, is real and safe. Editing or deleting a subtype is still fully
// unsupported (no endpoint at all). A domain value is different: editing
// its label/sort_order is always safe (pure display metadata, never in a
// CHECK), and deleting it is safe the same way adding one already was --
// see deleteDomainValue's own doc comment on why a later recompile fails
// loudly rather than silently narrowing real data out from under itself.
// An existing field's own domain/group assignment, nullable, and
// form-visibility flags are the other real exception: those are read
// live from metadata.fields by whatever consumes them (the CRUD engine,
// a following recompile), never baked into the physical column at ADD
// COLUMN time, so editing them in place is real and safe too -- see
// update.go's updateField. What's still genuinely unsupported: renaming
// or retyping a field, and editing/removing a subtype.
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
//	POST   /feature-classes/{code}/subtypes add one subtype to an existing class (any status) -- recompile to actually enforce it
//	POST   /feature-classes/{code}/fields  add one field to an existing class (any status) -- recompile to actually add the column
//	PATCH  /feature-classes/{code}/fields/{name} edit an existing field's domain/group/nullable/visibility -- never name or data_type
//	POST   /feature-classes/{code}/compile compile/recompile -- wraps api/internal/compiler.Compile directly
//	GET    /domains                        every domain with its values
//	POST   /domains                        create a domain, optionally with initial coded values
//	PATCH  /domains/{code}                 edit a domain's name, or (range domain only) its range_min/range_max
//	DELETE /domains/{code}                 delete a domain not referenced by any field
//	POST   /domains/{code}/values          add one value to an existing coded domain -- recompile any class using it to actually enforce it
//	PATCH  /domains/{code}/values/{value_code} edit an existing coded value's label/sort_order/material_spec_code
//	DELETE /domains/{code}/values/{value_code} delete a coded value -- recompile any class using it to actually narrow the CHECK
//	GET    /field-groups                   every field group
//	POST   /field-groups                   create a field group
//	DELETE /field-groups/{code}            delete a field group not referenced by any field
//	GET    /material-specs                 every material spec (e.g. ACSR_50) with its engineering numbers
//	POST   /material-specs                 create a material spec
//	PATCH  /material-specs/{code}          edit an existing material spec's own numbers
//	DELETE /material-specs/{code}          delete a material spec not referenced by any domain value
//	GET    /symbology-icons                every icon_ref -> svg row (the icon library)
//	POST   /symbology-icons                add one icon (name + svg markup)
//	PATCH  /symbology-icons/{icon_ref}     replace an icon's svg markup in place
//	DELETE /symbology-icons/{icon_ref}     delete an icon not referenced by any symbology rule
//	GET    /feature-classes/{code}/symbology-rules  every rule for a class, id + code-shaped targeting
//	POST   /feature-classes/{code}/symbology-rules  add one rule (targeting + style)
//	PATCH  /symbology-rules/{id}           replace a rule wholesale
//	DELETE /symbology-rules/{id}           delete a rule (no guard -- compiler re-validates the default at publish)
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/feature-classes", listFeatureClassesHandler(db))
	r.Post("/feature-classes", createFeatureClassHandler(db))
	r.Patch("/feature-classes/{code}", updateFeatureClassHandler(db))
	r.Delete("/feature-classes/{code}", deleteFeatureClassHandler(db))
	r.Post("/feature-classes/{code}/subtypes", addSubtypeHandler(db))
	r.Post("/feature-classes/{code}/fields", addFieldHandler(db))
	r.Patch("/feature-classes/{code}/fields/{name}", updateFieldHandler(db))
	r.Post("/feature-classes/{code}/compile", compileFeatureClassHandler(db))
	r.Get("/domains", listDomainsHandler(db))
	r.Post("/domains", createDomainHandler(db))
	r.Patch("/domains/{code}", updateDomainHandler(db))
	r.Delete("/domains/{code}", deleteDomainHandler(db))
	r.Post("/domains/{code}/values", addDomainValueHandler(db))
	r.Patch("/domains/{code}/values/{value_code}", updateDomainValueHandler(db))
	r.Delete("/domains/{code}/values/{value_code}", deleteDomainValueHandler(db))
	r.Get("/field-groups", listFieldGroupsHandler(db))
	r.Post("/field-groups", createFieldGroupHandler(db))
	r.Delete("/field-groups/{code}", deleteFieldGroupHandler(db))
	r.Get("/material-specs", listMaterialSpecsHandler(db))
	r.Post("/material-specs", createMaterialSpecHandler(db))
	r.Patch("/material-specs/{code}", updateMaterialSpecHandler(db))
	r.Delete("/material-specs/{code}", deleteMaterialSpecHandler(db))
	r.Get("/symbology-icons", listSymbologyIconsHandler(db))
	r.Post("/symbology-icons", createSymbologyIconHandler(db))
	r.Patch("/symbology-icons/{icon_ref}", updateSymbologyIconHandler(db))
	r.Delete("/symbology-icons/{icon_ref}", deleteSymbologyIconHandler(db))
	r.Get("/feature-classes/{code}/symbology-rules", listSymbologyRulesHandler(db))
	r.Post("/feature-classes/{code}/symbology-rules", createSymbologyRuleHandler(db))
	r.Patch("/symbology-rules/{id}", updateSymbologyRuleHandler(db))
	r.Delete("/symbology-rules/{id}", deleteSymbologyRuleHandler(db))
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

func addSubtypeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req subtypeRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := addSubtype(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func addFieldHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req fieldRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := addField(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateFieldHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		name := chi.URLParam(r, "name")
		var req updateFieldRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateField(r.Context(), db, code, name, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func addDomainValueHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req domainValueRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := addDomainValue(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateDomainHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req updateDomainRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateDomain(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func updateDomainValueHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		valueCode := chi.URLParam(r, "value_code")
		var req updateDomainValueRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateDomainValue(r.Context(), db, code, valueCode, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteDomainValueHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		valueCode := chi.URLParam(r, "value_code")
		if err := deleteDomainValue(r.Context(), db, code, valueCode); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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

func listMaterialSpecsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		specs, err := listMaterialSpecs(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"material_specs": specs})
	}
}

func createMaterialSpecHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createMaterialSpecRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createMaterialSpec(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateMaterialSpecHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req updateMaterialSpecRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateMaterialSpec(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteMaterialSpecHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if err := deleteMaterialSpec(r.Context(), db, code); err != nil {
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

func listSymbologyIconsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		icons, err := listSymbologyIcons(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"symbology_icons": icons})
	}
}

func createSymbologyIconHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createSymbologyIconRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createSymbologyIcon(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateSymbologyIconHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		iconRef := chi.URLParam(r, "icon_ref")
		var req updateSymbologyIconRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateSymbologyIcon(r.Context(), db, iconRef, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteSymbologyIconHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		iconRef := chi.URLParam(r, "icon_ref")
		if err := deleteSymbologyIcon(r.Context(), db, iconRef); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listSymbologyRulesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		rules, err := listSymbologyRules(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"symbology_rules": rules})
	}
}

func createSymbologyRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req symbologyRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createSymbologyRule(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateSymbologyRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req symbologyRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateSymbologyRule(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteSymbologyRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteSymbologyRule(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
