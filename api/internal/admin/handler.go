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
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/importer"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
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
//	PATCH  /feature-classes/{code}/fields/{name} edit an existing field's domain/group/nullable/visibility/required-per-subtype -- never name or data_type; required_for_subtypes takes effect live (no recompile)
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
//	GET    /symbology-stencils             every diagram stencil with its promote_class mapping
//	PATCH  /symbology-stencils/{icon_ref}  set/clear a stencil's promote_class (the feature class a stamped symbol promotes into)
//	GET    /feature-classes/{code}/symbology-rules  every rule for a class, id + code-shaped targeting
//	POST   /feature-classes/{code}/symbology-rules  add one rule (targeting + style)
//	PATCH  /symbology-rules/{id}           replace a rule wholesale
//	DELETE /symbology-rules/{id}           delete a rule (no guard -- compiler re-validates the default at publish)
//	GET    /diagram-placement-rules        every (container × diagram type) placement allow-list rule, code-shaped
//	POST   /diagram-placement-rules        allow one feature class on one (container × diagram type) pairing
//	DELETE /diagram-placement-rules/{id}   disallow one feature (remove one rule) -- emptying a pairing reverts it to unconstrained
//	POST   /network-rules                  add one network (connectivity) rule -- from/to class + optional terminals, phase rule, valid flag
//	DELETE /network-rules/{id}             remove one network rule
//	POST   /relationship-classes           add one relationship class -- origin/destination + kind + cardinality
//	DELETE /relationship-classes/{id}      remove one relationship class
//	POST   /snapping-rules                 add one snapping rule -- from/to class + optional terminal, target type, tolerance, priority, required flag (display-only until the Snap UI consumes it)
//	DELETE /snapping-rules/{id}            remove one snapping rule
//	GET    /cim-profiles                   every CIM export profile (Phase 55)
//	POST   /cim-profiles                   create a CIM export profile
//	PATCH  /cim-profiles/{code}            edit a CIM profile (all columns NOT NULL, set-only)
//	DELETE /cim-profiles/{code}            delete a CIM profile
//	GET    /cim-class-mappings             every feature-class -> CIM-class mapping, code-shaped, id in each row
//	POST   /cim-class-mappings             map a feature class (optionally one subtype) to a CIM class + kind
//	PATCH  /cim-class-mappings/{id}        edit a class mapping's cim_class/cim_kind
//	DELETE /cim-class-mappings/{id}        delete a class mapping
//	GET    /cim-attribute-mappings         every field -> CIM-attribute mapping, code-shaped, id in each row
//	POST   /cim-attribute-mappings         map a feature field to a CIM attribute + transform
//	PATCH  /cim-attribute-mappings/{id}    edit an attribute mapping's cim_attribute/transform/scale_factor/unit/value_map
//	DELETE /cim-attribute-mappings/{id}    delete an attribute mapping
//	GET    /cim-coverage                   feature classes with no CIM class mapping (silently dropped from export)
//	GET    /cim-preview/{code}             one real feature of a class rendered through the CIM encoder (byte-exact CIM/XML preview; ?profile= optional)
//	GET    /cim-dictionary/classes         the CIM reference dictionary's classes, each flagged in_profile (mig 0120; ?profile= optional)
//	GET    /cim-dictionary/classes/{name}/attributes   the reference attributes of one CIM class, each flagged in_profile (?profile= optional)
//	GET    /import-profiles                every ETL import profile, code-shaped (Phase / docs/etl-import-engine.md)
//	POST   /import-profiles                create an import profile (target class + write policy + geometry mode)
//	PATCH  /import-profiles/{code}         edit an import profile (name/format/write policy/geometry/status; not its target class)
//	DELETE /import-profiles/{code}         delete an import profile (its field mappings cascade)
//	POST   /import-profiles/{code}/test-connection  open+ping a live-DB profile's saved connection
//	GET    /import-profiles/{code}/tables  list a live-DB profile's remote tables (source-table picker)
//	GET    /import-profiles/{code}/field-mappings  every source-column -> target-field mapping of a profile
//	POST   /import-profiles/{code}/field-mappings  add one field mapping (source column + transform) to a profile
//	PATCH  /import-field-mappings/{id}     edit a field mapping's source_column/transform/parameter
//	DELETE /import-field-mappings/{id}     delete a field mapping
//
// cipher seals/unseals a live-DB profile's stored password (v2-S3); it is nil
// when credential encryption is not configured, in which case saving a profile
// with a password (or testing a saved one) fails with a clear error.
func Routes(db *bun.DB, cipher *secretbox.Cipher) chi.Router {
	r := chi.NewRouter()
	r.Get("/feature-classes", listFeatureClassesHandler(db))
	r.Post("/feature-classes", createFeatureClassHandler(db))
	r.Patch("/feature-classes/{code}", updateFeatureClassHandler(db))
	r.Delete("/feature-classes/{code}", deleteFeatureClassHandler(db))
	r.Post("/feature-classes/{code}/subtypes", addSubtypeHandler(db))
	r.Post("/feature-classes/{code}/fields", addFieldHandler(db))
	r.Patch("/feature-classes/{code}/fields/{name}", updateFieldHandler(db))
	r.Post("/feature-classes/{code}/compile", compileFeatureClassHandler(db))
	r.Post("/feature-classes/{code}/uncompile", uncompileFeatureClassHandler(db))
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
	r.Get("/symbology-stencils", listDiagramStencilsHandler(db))
	r.Patch("/symbology-stencils/{icon_ref}", updateDiagramStencilHandler(db))
	r.Get("/feature-classes/{code}/symbology-rules", listSymbologyRulesHandler(db))
	r.Post("/feature-classes/{code}/symbology-rules", createSymbologyRuleHandler(db))
	r.Patch("/symbology-rules/{id}", updateSymbologyRuleHandler(db))
	r.Delete("/symbology-rules/{id}", deleteSymbologyRuleHandler(db))
	r.Get("/diagram-placement-rules", listDiagramPlacementRulesHandler(db))
	r.Post("/diagram-placement-rules", createDiagramPlacementRuleHandler(db))
	r.Delete("/diagram-placement-rules/{id}", deleteDiagramPlacementRuleHandler(db))
	r.Post("/network-rules", createNetworkRuleHandler(db))
	r.Delete("/network-rules/{id}", deleteNetworkRuleHandler(db))
	r.Post("/relationship-classes", createRelationshipClassHandler(db))
	r.Delete("/relationship-classes/{id}", deleteRelationshipClassHandler(db))
	r.Post("/snapping-rules", createSnappingRuleHandler(db))
	r.Delete("/snapping-rules/{id}", deleteSnappingRuleHandler(db))
	r.Get("/cim-profiles", listCimProfilesHandler(db))
	r.Post("/cim-profiles", createCimProfileHandler(db))
	r.Patch("/cim-profiles/{code}", updateCimProfileHandler(db))
	r.Delete("/cim-profiles/{code}", deleteCimProfileHandler(db))
	r.Get("/cim-class-mappings", listCimClassMappingsHandler(db))
	r.Post("/cim-class-mappings", createCimClassMappingHandler(db))
	r.Patch("/cim-class-mappings/{id}", updateCimClassMappingHandler(db))
	r.Delete("/cim-class-mappings/{id}", deleteCimClassMappingHandler(db))
	r.Get("/cim-attribute-mappings", listCimAttributeMappingsHandler(db))
	r.Post("/cim-attribute-mappings", createCimAttributeMappingHandler(db))
	r.Patch("/cim-attribute-mappings/{id}", updateCimAttributeMappingHandler(db))
	r.Delete("/cim-attribute-mappings/{id}", deleteCimAttributeMappingHandler(db))
	r.Get("/cim-coverage", listCimCoverageHandler(db))
	r.Get("/cim-preview/{code}", getCimPreviewHandler(db))
	r.Get("/cim-dictionary/classes", listCimRefClassesHandler(db))
	r.Get("/cim-dictionary/classes/{name}/attributes", listCimRefAttributesHandler(db))
	r.Get("/import-profiles", listImportProfilesHandler(db))
	r.Post("/import-profiles", createImportProfileHandler(db, cipher))
	r.Patch("/import-profiles/{code}", updateImportProfileHandler(db, cipher))
	r.Delete("/import-profiles/{code}", deleteImportProfileHandler(db))
	r.Post("/import-profiles/{code}/test-connection", testImportConnectionHandler(db, cipher))
	r.Get("/import-profiles/{code}/tables", listImportTablesHandler(db, cipher))
	r.Get("/import-profiles/{code}/field-mappings", listImportFieldMappingsHandler(db))
	r.Post("/import-profiles/{code}/field-mappings", createImportFieldMappingHandler(db))
	r.Patch("/import-field-mappings/{id}", updateImportFieldMappingHandler(db))
	r.Delete("/import-field-mappings/{id}", deleteImportFieldMappingHandler(db))
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
	// importer.IsInvalid / IsNotFound cover errors bubbled up from the importer
	// package (live-DB connect/test/list, ResolveProfile) -- the admin editor's
	// live-DB actions surface these, and they're the same 400/404 shape.
	case isValidationError(err), importer.IsInvalid(err):
		status = http.StatusBadRequest
	case isNotFoundError(err), importer.IsNotFound(err):
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

// uncompileFeatureClassHandler wraps compiler.Uncompile directly -- the
// inverse of compileFeatureClassHandler, reachable over HTTP so an admin
// can drop a class's physical table (and its tile function) and reset it
// to draft without a terminal. This is the recovery for the additive
// compiler's blind spot: adding a required (NOT NULL) field to a class
// that already has rows fails (Postgres SQLSTATE 23502), so the table must
// be dropped and recompiled fresh. DESTRUCTIVE -- it discards every row;
// the response's rows_dropped says how many, and the UI confirms first.
func uncompileFeatureClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		result, err := compiler.Uncompile(r.Context(), db, code)
		if err != nil {
			writeError(w, invalid("uncompile %s: %v", code, err))
			return
		}
		writeJSON(w, http.StatusOK, uncompileResponse{
			FeatureClassCode: result.FeatureClassCode,
			PhysicalTable:    result.PhysicalTable,
			TableExisted:     result.TableExisted,
			RowsDropped:      result.RowsDropped,
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

func listDiagramStencilsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stencils, err := listDiagramStencils(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"stencils": stencils})
	}
}

func updateDiagramStencilHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		iconRef := chi.URLParam(r, "icon_ref")
		var req updateDiagramStencilRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateDiagramStencil(r.Context(), db, iconRef, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
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

func listDiagramPlacementRulesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rules, err := listDiagramPlacementRules(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"diagram_placement_rules": rules})
	}
}

func createDiagramPlacementRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req diagramPlacementRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createDiagramPlacementRule(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func deleteDiagramPlacementRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteDiagramPlacementRule(r.Context(), db, id); err != nil {
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

func createNetworkRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req networkRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createNetworkRule(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func deleteNetworkRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteNetworkRule(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func createRelationshipClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req relationshipClassRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createRelationshipClass(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func deleteRelationshipClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteRelationshipClass(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func createSnappingRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req snappingRuleRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createSnappingRule(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func deleteSnappingRuleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteSnappingRule(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- CIM export mapping (Phase 55) ----------------------------------------

func listCimProfilesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profiles, err := listCimProfiles(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cim_profiles": profiles})
	}
}

func createCimProfileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createCimProfileRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createCimProfile(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateCimProfileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req updateCimProfileRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateCimProfile(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteCimProfileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if err := deleteCimProfile(r.Context(), db, code); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listCimClassMappingsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mappings, err := listCimClassMappings(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cim_class_mappings": mappings})
	}
}

func createCimClassMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createCimClassMappingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createCimClassMapping(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateCimClassMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateCimClassMappingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateCimClassMapping(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteCimClassMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteCimClassMapping(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listCimAttributeMappingsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mappings, err := listCimAttributeMappings(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cim_attribute_mappings": mappings})
	}
}

func createCimAttributeMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createCimAttributeMappingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createCimAttributeMapping(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateCimAttributeMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateCimAttributeMappingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateCimAttributeMapping(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteCimAttributeMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteCimAttributeMapping(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listCimCoverageHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		classes, err := listCimCoverage(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"unmapped_classes": classes})
	}
}

// getCimPreviewHandler renders one real feature of the class through the actual
// CIM encoder (export.PreviewFeature) so the editor can show the byte-exact
// CIM/XML a full export produces for it, not just the mapping rule table. The
// optional ?profile= query names a non-default CIM profile. A geometry-less
// class or one with no rows is not an error -- the response carries sampled=false
// with a note.
func getCimPreviewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		preview, err := export.PreviewFeature(r.Context(), db, code, r.URL.Query().Get("profile"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cimPreviewResponse{
			ClassCode: preview.ClassCode,
			FeatureID: preview.FeatureID,
			Xml:       preview.XML,
			Sampled:   preview.Sampled,
			Note:      preview.Note,
		})
	}
}

// listCimRefClassesHandler serves the CIM reference dictionary's classes (mig
// 0120) so the mapping editor can offer real CIM class names and flag free text
// that isn't in the vocabulary. The optional ?profile= sets which profile's
// membership decides in_profile (empty -> the default profile).
func listCimRefClassesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		classes, err := listCimRefClasses(r.Context(), db, r.URL.Query().Get("profile"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cim_ref_classes": classes})
	}
}

// listCimRefAttributesHandler serves the reference attributes of one CIM class
// (path {name}) so the editor can offer the real properties of a chosen CIM
// class. An unknown class yields an empty list, not an error. ?profile= as above.
func listCimRefAttributesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		attrs, err := listCimRefAttributes(r.Context(), db, name, r.URL.Query().Get("profile"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cim_ref_attributes": attrs})
	}
}
