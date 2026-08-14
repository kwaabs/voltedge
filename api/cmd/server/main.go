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
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/diagram"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
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

	r.Get("/healthz", healthzHandler(db))
	r.Get("/feature-classes", featureClassesHandler(db))
	r.Get("/feature-classes/{code}/schema", featureClassSchemaHandler(db))
	r.Mount("/features", crud.Routes(db))
	r.Mount("/connectivity", connectivity.Routes(db))
	r.Mount("/relationships", relationship.Routes(db))
	r.Mount("/diagrams", diagram.Routes(db))
	r.Mount("/trace", trace.Routes(db))

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
	Code         string `json:"code"`
	Name         string `json:"name"`
	GeometryType string `json:"geometry_type"`
	NetworkRole  string `json:"network_role"`
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
				Code:         fc.Code,
				Name:         fc.Name,
				GeometryType: fc.GeometryType,
				NetworkRole:  fc.NetworkRole,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"feature_classes": out})
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
	RequiredForSubtypes  []int    `json:"required_for_subtypes,omitempty"`
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
type symbologySchema struct {
	SubtypeCode     *int            `json:"subtype_code,omitempty"`
	DomainField     *string         `json:"domain_field,omitempty"`
	DomainValueCode *string         `json:"domain_value_code,omitempty"`
	StyleDef        json.RawMessage `json:"style_def"`
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

// resolveSymbologySchema loads a feature class's map-context symbology
// rules and resolves each one's domain_value_id into a (field name,
// value code) pair the frontend's matcher can compare directly against
// a rendered feature's own attributes -- see symbologySchema's own
// comment for why that resolution belongs here, not in the frontend.
func resolveSymbologySchema(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, subtypeCodeByID map[string]int) ([]symbologySchema, error) {
	rules, err := metadata.LoadSymbologyRules(ctx, db, loaded.FeatureClass.ID)
	if err != nil {
		return nil, err
	}

	valueIDs := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.RenderContext == "map" && r.DomainValueID != nil {
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
		if r.RenderContext != "map" {
			continue
		}
		s := symbologySchema{StyleDef: r.StyleDef}
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
