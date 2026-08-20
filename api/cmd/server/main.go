// Command server is the VoltEdge API. Phase 0 gave it a healthcheck;
// Phase 2 adds the generic, metadata-driven CRUD engine (api/internal/crud)
// -- one set of handlers that serves every compiled feature class, no
// feature-specific code required -- plus two explicit-confirm engines:
// zero-trust electrical connectivity (api/internal/connectivity) and
// structural/spatial relationships (api/internal/relationship), the
// only paths that turn a candidate connection or relationship into
// real topology/fact. Phase 4 adds the read-only network trace engine
// (api/internal/trace) that walks that same committed topology.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kwaabs/voltedge/api/internal/admin"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/diagram"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/powerflow"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/kwaabs/voltedge/api/internal/rules"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Phase 10: the trust boundary. SecurityHeaders hardens every response;
	// SessionLoader validates the session cookie (if any) and attaches the
	// authenticated user -- and their effective permission set -- to the
	// request context. SessionLoader itself is non-failing (an anonymous
	// request flows through); it is the per-route RBAC gates below (Slice C)
	// that actually reject: 401 without a session, 403 without the permission.
	authCfg := auth.NewConfigFromEnv()
	r.Use(auth.SecurityHeaders())
	r.Use(auth.SessionLoader(db, authCfg))

	// /healthz stays public -- container orchestration probes it with no
	// session, and it exposes nothing but DB reachability.
	r.Get("/healthz", healthzHandler(db))

	// The map/forms read layer: the feature-class catalog, per-class schema
	// and rules, and the global symbology icon set. All GET, all gated by
	// features:read -- any viewer must load these to see the map at all.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead))
		r.Get("/feature-classes", featureClassesHandler(db))
		r.Get("/feature-classes/{code}/schema", featureClassSchemaHandler(db))
		r.Get("/feature-classes/{code}/rules", rules.Handler(db))
		r.Get("/symbology/icons", symbologyIconsHandler(db))
	})

	// Domain mounts, each wrapped in an RBAC gate. Mixed read/write mounts
	// get a method-aware gate (GET => the :read permission, mutating => the
	// :write/:commit one); the pure-action engines (trace, analysis), whose
	// POSTs are queries rather than writes, get a single permission for
	// every method. /auth is not wrapped here -- it self-gates (login and
	// the SSO endpoints are public by necessity; logout/me/settings carry
	// their own RequireAuth/RequireCSRF, and the switch checks
	// admin:auth-settings/break-glass itself).
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/features", crud.Routes(db))
	// Territories are fixed reference data read alongside features (the
	// feature panel resolves a feature's territory_id to a name via this
	// list), so the read-only endpoint sits under the same features:read
	// gate every map viewer already holds -- not the metadata admin gate.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermTerritoriesWrite)).Mount("/territories", territory.Routes(db))
	r.With(auth.RequirePermission(auth.PermConnectivityRead, auth.PermConnectivityCommit)).Mount("/connectivity", connectivity.Routes(db))
	r.With(auth.RequirePermission(auth.PermRelationshipsRead, auth.PermRelationshipsCommit)).Mount("/relationships", relationship.Routes(db))
	r.With(auth.RequirePermission(auth.PermDiagramsRead, auth.PermDiagramsWrite)).Mount("/diagrams", diagram.Routes(db))
	r.With(auth.RequirePermission(auth.PermTraceRun, auth.PermTraceRun)).Mount("/trace", trace.Routes(db))
	r.With(auth.RequirePermission(auth.PermAnalysisRun, auth.PermAnalysisRun)).Mount("/analysis", powerflow.Routes(db))
	r.With(auth.RequirePermission(auth.PermMetadataRead, auth.PermMetadataWrite)).Mount("/admin", admin.Routes(db))
	r.Mount("/auth", auth.Routes(db, authCfg))

	log.Printf("voltedge api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}

// healthzHandler proves the api container can actually reach Postgres, not
// just that the process is running -- a plain 200 with no DB check would
// hide a broken DATABASE_URL until the first real request fails.
func healthzHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		status := "ok"
		code := http.StatusOK

		if err := db.PingContext(ctx); err != nil {
			status = "db unreachable: " + err.Error()
			code = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
}

// featureClassResponse is deliberately its own type, not
// metadata.FeatureClass serialized directly -- that type has no json
// tags (nothing before this needed it over the wire) and carries a
// couple of internal-only fields (MetadataVersionID) with no reason to
// ever leave the API.
type featureClassResponse struct {
	Code               string `json:"code"`
	Name               string `json:"name"`
	GeometryType       string `json:"geometry_type"`
	NetworkRole        string `json:"network_role"`
	IsDiagramContainer bool   `json:"is_diagram_container"`
}

// featureClassesHandler is what lets the frontend show a real,
// admin-set display name (e.g. "MV Conductor") instead of the bare
// table/tile-source id (e.g. "mv_conductor") Martin's catalog only
// knows -- see docs/phase-3-map-loop.md's follow-up on layer naming.
func featureClassesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		classes, err := metadata.ListFeatureClasses(r.Context(), db)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		out := make([]featureClassResponse, len(classes))
		for i, fc := range classes {
			out[i] = featureClassResponse{
				Code:               fc.Code,
				Name:               fc.Name,
				GeometryType:       fc.GeometryType,
				NetworkRole:        fc.NetworkRole,
				IsDiagramContainer: fc.IsDiagramContainer,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"feature_classes": out})
	}
}

// symbologyIcon is one symbology_icons row over the wire -- the SVG
// markup behind an icon_ref a symbology rule references. Its own type
// rather than metadata.SymbologyIcon serialized directly, matching how
// this file keeps wire-shaped copies of everything else it returns.
type symbologyIcon struct {
	IconRef string `json:"icon_ref"`
	SVG     string `json:"svg"`
}

// symbologyIconsHandler serves the whole icon set at once -- the frontend
// (web/src/hooks/use-symbology-icons.ts) fetches it once and resolves
// every rule's icon_ref against it client-side, the same way it already
// resolves symbology_rules. A small, global set, so no per-class scoping
// (unlike the per-feature-class schema endpoint); Martin never sees it.
func symbologyIconsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		icons, err := metadata.ListSymbologyIcons(r.Context(), db)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		out := make([]symbologyIcon, len(icons))
		for i, ic := range icons {
			out[i] = symbologyIcon{IconRef: ic.IconRef, SVG: ic.SVG}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"icons": out})
	}
}

// subtypeSchema is one row of a feature class's subtypes, code + name
// only -- everything a form needs to offer a subtype picker.
type subtypeSchema struct {
	Code int    `json:"code"`
	Name string `json:"name"`
}

// fieldSchema is everything a client needs to render one correct form
// control for one field, and to know when it's required, without
// re-deriving api/internal/crud's validation rules independently.
// RequiredForSubtypes is flattened from metadata.subtype_field_rules
// here rather than shipped as raw rule rows, since the only thing a
// form actually needs from that table is "is this required for the
// subtype the user picked" -- a field with a default_override for a
// given subtype is deliberately left out of that subtype's list, same
// as api/internal/crud/validate.go's validateRequired: the rule is
// satisfied by the fallback value, not by demanding the client send one.
type fieldSchema struct {
	Name                 string   `json:"name"`
	DataType             string   `json:"data_type"`
	Nullable             bool     `json:"nullable"`
	VisibleOnCreate      bool     `json:"visible_on_create"`
	VisibleOnUpdate      bool     `json:"visible_on_update"`
	EditableAfterPublish bool     `json:"editable_after_publish"`
	DomainCode           *string  `json:"domain_code,omitempty"`
	DomainValues         []string `json:"domain_values,omitempty"`
	RangeMin             *float64 `json:"range_min,omitempty"`
	RangeMax             *float64 `json:"range_max,omitempty"`
	RequiredForSubtypes  []int    `json:"required_for_subtypes,omitempty"`
	// MaterialSpecs is keyed by domain value code, only present for the
	// subset of DomainValues that carry a material_spec_id
	// (metadata.domain_values, migrations/0021) -- lets a consumer go
	// straight from "this field's current value is ACSR_50" to its real
	// engineering numbers (the map feature panel's own Materials tab)
	// without a second, admin-only round trip.
	MaterialSpecs map[string]materialSpecSummary `json:"material_specs,omitempty"`
}

// materialSpecSummary mirrors admin's own materialSpecResponse --
// duplicated rather than imported, the same way this package already
// keeps its own wire-shaped copies of everything else it returns
// (compileResponse mirrors compiler.Result for the identical reason,
// see api/internal/admin/types.go).
type materialSpecSummary struct {
	Code               string   `json:"code"`
	Name               string   `json:"name"`
	Category           string   `json:"category"`
	ResistanceOhmPerKm *float64 `json:"resistance_ohm_per_km,omitempty"`
	ReactanceOhmPerKm  *float64 `json:"reactance_ohm_per_km,omitempty"`
	AmpacityAmps       *float64 `json:"ampacity_amps,omitempty"`
	DiameterMm         *float64 `json:"diameter_mm,omitempty"`
	WeightKgPerKm      *float64 `json:"weight_kg_per_km,omitempty"`
}

// terminalSchema is one named connection point a junction feature class
// defines (e.g. DIST_XFMR's T1/T2/T3) -- everything a connectivity UI
// needs to offer a terminal picker without re-deriving
// metadata.Loaded.ResolveTerminal's subtype-scoping itself. SubtypeCode
// nil means the terminal applies to every subtype of the feature class
// (metadata.Terminal.SubtypeID nil, same meaning); a non-nil value
// scopes it to one specific subtype, and per ResolveTerminal's own
// most-specific-wins rule, a subtype-scoped terminal with the same code
// as a generic one takes precedence for that subtype.
type terminalSchema struct {
	Code         string `json:"code"`
	Role         string `json:"role"`
	PhaseCapable bool   `json:"phase_capable"`
	SubtypeCode  *int   `json:"subtype_code,omitempty"`
}

// symbologySchema is one symbology_rules row, pre-resolved for the
// frontend's own most-specific-wins matcher (web/src/lib/symbology.ts)
// -- SubtypeCode/DomainField/DomainValueCode nil means that axis is
// unconstrained (matches any subtype / any attribute value). DomainField
// is resolved server-side rather than making the frontend cross-reference
// domains itself: a rule only ever stores a domain_value_id, and "which
// field is implied by which domain_value" requires matching that value's
// domain against whichever of the feature class's fields uses it (see
// migrations/0012's own column comment) -- work this handler is already
// positioned to do once, not work worth repeating in every consumer.
// RenderContext ("map" or "diagram") is included, not filtered server-side
// -- both the map layer styling and the diagram canvas resolve from this
// same array client-side, each ignoring the rules meant for the other
// context, matching docs/metadata-core.md's "every icon decision happens
// client-side" design.
type symbologySchema struct {
	RenderContext   string          `json:"render_context"`
	SubtypeCode     *int            `json:"subtype_code,omitempty"`
	DomainField     *string         `json:"domain_field,omitempty"`
	DomainValueCode *string         `json:"domain_value_code,omitempty"`
	StyleDef        json.RawMessage `json:"style_def"`
	// MinZoom/MaxZoom were already real columns loaded into
	// metadata.SymbologyRule (see models.go) but never left this
	// package until now -- resolveSymbology (web/src/lib/symbology.ts)
	// couldn't zoom-gate a rule it never received.
	MinZoom *int `json:"min_zoom,omitempty"`
	MaxZoom *int `json:"max_zoom,omitempty"`
}

// featureClassSchemaResponse is the shape a create/edit form -- or a
// connectivity picker -- needs: whether (and what type of) geometry to
// draw, what subtype to pick (if any), what fields to render for it,
// and what terminals (if any) it can be connected through. Deliberately
// separate from featureClassResponse above -- that one backs the layers
// panel and map (cheap, always-fetched summary data); this one is
// heavier (every field + domain values + terminals) and only fetched
// when a form or picker is actually about to be shown.
type featureClassSchemaResponse struct {
	Code         string            `json:"code"`
	Name         string            `json:"name"`
	GeometryType string            `json:"geometry_type"`
	Subtypes     []subtypeSchema   `json:"subtypes"`
	Fields       []fieldSchema     `json:"fields"`
	Terminals    []terminalSchema  `json:"terminals,omitempty"`
	Symbology    []symbologySchema `json:"symbology,omitempty"`
}

// resolveSymbologySchema loads every one of a feature class's symbology
// rules (map and diagram context alike) and resolves each one's
// domain_value_id into a (field name, value code) pair the frontend's
// matcher can compare directly against a rendered feature's own
// attributes -- see symbologySchema's own comment for why that
// resolution belongs here, not in the frontend.
func resolveSymbologySchema(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, subtypeCodeByID map[string]int) ([]symbologySchema, error) {
	rules, err := metadata.LoadSymbologyRules(ctx, db, loaded.FeatureClass.ID)
	if err != nil {
		return nil, err
	}

	valueIDs := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.DomainValueID != nil {
			valueIDs = append(valueIDs, *r.DomainValueID)
		}
	}
	var values []metadata.DomainValue
	if len(valueIDs) > 0 {
		if err := db.NewSelect().Model(&values).Where("id IN (?)", bun.In(valueIDs)).Scan(ctx); err != nil {
			return nil, err
		}
	}
	valueByID := make(map[string]metadata.DomainValue, len(values))
	for _, v := range values {
		valueByID[v.ID] = v
	}

	fieldNameByDomainID := make(map[string]string, len(loaded.Fields))
	for _, f := range loaded.Fields {
		if f.DomainID != nil {
			fieldNameByDomainID[*f.DomainID] = f.Name
		}
	}

	out := make([]symbologySchema, 0, len(rules))
	for _, r := range rules {
		s := symbologySchema{StyleDef: r.StyleDef, RenderContext: r.RenderContext, MinZoom: r.MinZoom, MaxZoom: r.MaxZoom}
		if r.SubtypeID != nil {
			if code, ok := subtypeCodeByID[*r.SubtypeID]; ok {
				s.SubtypeCode = &code
			}
		}
		if r.DomainValueID != nil {
			if v, ok := valueByID[*r.DomainValueID]; ok {
				if fieldName, ok := fieldNameByDomainID[v.DomainID]; ok {
					s.DomainField = &fieldName
				}
				code := v.Code
				s.DomainValueCode = &code
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// resolveMaterialSpecs looks up every domain_values row under domainID
// that carries a material_spec_id, and resolves each into a full
// materialSpecSummary keyed by the domain value's own code. Returns nil
// (not an empty map) when nothing under this domain is material-linked,
// so fieldSchema.MaterialSpecs' own omitempty leaves it out of the
// response entirely for the common case (most domains carry no
// material data at all).
func resolveMaterialSpecs(ctx context.Context, db *bun.DB, domainID string) (map[string]materialSpecSummary, error) {
	var values []metadata.DomainValue
	if err := db.NewSelect().Model(&values).Where("domain_id = ? AND material_spec_id IS NOT NULL", domainID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading material-linked domain values: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}

	specIDs := make([]string, len(values))
	for i, v := range values {
		specIDs[i] = *v.MaterialSpecID
	}
	var specs []metadata.MaterialSpec
	if err := db.NewSelect().Model(&specs).Where("id IN (?)", bun.In(specIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading material specs: %w", err)
	}
	specByID := make(map[string]metadata.MaterialSpec, len(specs))
	for _, s := range specs {
		specByID[s.ID] = s
	}

	out := make(map[string]materialSpecSummary, len(values))
	for _, v := range values {
		s, ok := specByID[*v.MaterialSpecID]
		if !ok {
			continue
		}
		out[v.Code] = materialSpecSummary{
			Code: s.Code, Name: s.Name, Category: s.Category,
			ResistanceOhmPerKm: s.ResistanceOhmPerKm, ReactanceOhmPerKm: s.ReactanceOhmPerKm,
			AmpacityAmps: s.AmpacityAmps, DiameterMm: s.DiameterMm, WeightKgPerKm: s.WeightKgPerKm,
		}
	}
	return out, nil
}

func featureClassSchemaHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")

		loaded, err := metadata.Load(r.Context(), db, code)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		subtypes := make([]subtypeSchema, len(loaded.Subtypes))
		for i, s := range loaded.Subtypes {
			subtypes[i] = subtypeSchema{Code: s.Code, Name: s.Name}
		}

		fields := make([]fieldSchema, len(loaded.Fields))
		for i, f := range loaded.Fields {
			fs := fieldSchema{
				Name:                 f.Name,
				DataType:             f.DataType,
				Nullable:             f.Nullable,
				VisibleOnCreate:      f.VisibleOnCreate,
				VisibleOnUpdate:      f.VisibleOnUpdate,
				EditableAfterPublish: f.EditableAfterPublish,
			}

			if f.DomainID != nil {
				if d, ok := loaded.Domains[*f.DomainID]; ok {
					code := d.Code
					fs.DomainCode = &code
					if d.DomainType == "coded" {
						fs.DomainValues = loaded.DomainValues[d.ID]
						materialSpecs, err := resolveMaterialSpecs(r.Context(), db, d.ID)
						if err != nil {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusInternalServerError)
							json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
							return
						}
						fs.MaterialSpecs = materialSpecs
					} else if d.DomainType == "range" {
						fs.RangeMin = d.RangeMin
						fs.RangeMax = d.RangeMax
					}
				}
			}

			for _, st := range loaded.Subtypes {
				for _, rule := range loaded.SubtypeFieldRules {
					if rule.SubtypeID == st.ID && rule.FieldID == f.ID && rule.IsRequired && rule.DefaultOverride == nil {
						fs.RequiredForSubtypes = append(fs.RequiredForSubtypes, st.Code)
					}
				}
			}

			fields[i] = fs
		}

		subtypeCodeByID := make(map[string]int, len(loaded.Subtypes))
		for _, st := range loaded.Subtypes {
			subtypeCodeByID[st.ID] = st.Code
		}

		terminals := make([]terminalSchema, len(loaded.Terminals))
		for i, t := range loaded.Terminals {
			ts := terminalSchema{Code: t.Code, Role: t.Role, PhaseCapable: t.PhaseCapable}
			if t.SubtypeID != nil {
				if code, ok := subtypeCodeByID[*t.SubtypeID]; ok {
					ts.SubtypeCode = &code
				}
			}
			terminals[i] = ts
		}

		symbology, err := resolveSymbologySchema(r.Context(), db, loaded, subtypeCodeByID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(featureClassSchemaResponse{
			Code:         loaded.FeatureClass.Code,
			Name:         loaded.FeatureClass.Name,
			GeometryType: loaded.FeatureClass.GeometryType,
			Subtypes:     subtypes,
			Fields:       fields,
			Terminals:    terminals,
			Symbology:    symbology,
		})
	}
}
