package schematic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// OnelineOverride is one operator position nudge for the live operations one-line
// (migration 0200). It is operations-owned display state, not a GIS diagram: it
// shifts where a feature is drawn in the one-line's abstract coordinate space, on top
// of the deterministic computed layout, and never touches the feature's real geometry.
type OnelineOverride struct {
	bun.BaseModel `bun:"table:ops.oneline_overrides,alias:oneline_overrides"`

	FeatureClass string    `bun:"feature_class,pk" json:"feature_class"`
	FeatureID    string    `bun:"feature_id,pk" json:"feature_id"`
	X            float64   `bun:"x" json:"x"`
	Y            float64   `bun:"y" json:"y"`
	UpdatedBy    *string   `bun:"updated_by,nullzero" json:"-"`
	UpdatedAt    time.Time `bun:"updated_at,nullzero" json:"-"`
}

// ListOverrides returns every operator position nudge.
func ListOverrides(ctx context.Context, db bun.IDB) ([]OnelineOverride, error) {
	rows := []OnelineOverride{}
	if err := db.NewSelect().Model(&rows).Scan(ctx); err != nil {
		return nil, fmt.Errorf("schematic: list overrides: %w", err)
	}
	// Never nil -> the JSON is always [] not null (a nil slice marshals to null).
	if rows == nil {
		rows = []OnelineOverride{}
	}
	return rows, nil
}

// UpsertOverride saves (or moves) one feature's nudge.
func UpsertOverride(ctx context.Context, db bun.IDB, o *OnelineOverride) error {
	o.UpdatedAt = time.Now().UTC()
	if _, err := db.NewInsert().Model(o).
		On("CONFLICT (feature_class, feature_id) DO UPDATE").
		Set("x = EXCLUDED.x, y = EXCLUDED.y, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at").
		Exec(ctx); err != nil {
		return fmt.Errorf("schematic: upsert override: %w", err)
	}
	return nil
}

// ClearOverrides drops every nudge -- "Reset layout" back to the pure computed layout.
func ClearOverrides(ctx context.Context, db bun.IDB) error {
	if _, err := db.NewDelete().Model((*OnelineOverride)(nil)).Where("1 = 1").Exec(ctx); err != nil {
		return fmt.Errorf("schematic: clear overrides: %w", err)
	}
	return nil
}

// WireOverride is one operator's hand-routing of a wire on the live operations one-line
// (migration 0202): the INTERMEDIATE bend vertices between its two nodes. The renderer
// composes the full path as [live start node, ...Vertices, live end node], so the
// endpoints follow the nodes while the bends stick. WireKey is the client-derived stable
// key (a conductor feature_id, or "link:<sorted node pair>").
type WireOverride struct {
	bun.BaseModel `bun:"table:ops.oneline_wire_overrides,alias:oneline_wire_overrides"`

	WireKey   string          `bun:"wire_key,pk" json:"wire_key"`
	Vertices  json.RawMessage `bun:"vertices,type:jsonb" json:"vertices"`
	UpdatedBy *string         `bun:"updated_by,nullzero" json:"-"`
	UpdatedAt time.Time       `bun:"updated_at,nullzero" json:"-"`
}

// ListWireOverrides returns every hand-routed wire.
func ListWireOverrides(ctx context.Context, db bun.IDB) ([]WireOverride, error) {
	rows := []WireOverride{}
	if err := db.NewSelect().Model(&rows).Scan(ctx); err != nil {
		return nil, fmt.Errorf("schematic: list wire overrides: %w", err)
	}
	if rows == nil {
		rows = []WireOverride{}
	}
	return rows, nil
}

// UpsertWireOverride saves one wire's bend vertices.
func UpsertWireOverride(ctx context.Context, db bun.IDB, o *WireOverride) error {
	o.UpdatedAt = time.Now().UTC()
	if _, err := db.NewInsert().Model(o).
		On("CONFLICT (wire_key) DO UPDATE").
		Set("vertices = EXCLUDED.vertices, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at").
		Exec(ctx); err != nil {
		return fmt.Errorf("schematic: upsert wire override: %w", err)
	}
	return nil
}

// DeleteWireOverride drops one wire's hand-routing (back to the auto route).
func DeleteWireOverride(ctx context.Context, db bun.IDB, wireKey string) error {
	if _, err := db.NewDelete().Model((*WireOverride)(nil)).Where("wire_key = ?", wireKey).Exec(ctx); err != nil {
		return fmt.Errorf("schematic: delete wire override: %w", err)
	}
	return nil
}

// ClearWireOverrides drops every hand-routed wire.
func ClearWireOverrides(ctx context.Context, db bun.IDB) error {
	if _, err := db.NewDelete().Model((*WireOverride)(nil)).Where("1 = 1").Exec(ctx); err != nil {
		return fmt.Errorf("schematic: clear wire overrides: %w", err)
	}
	return nil
}

// OverrideRoutes serves the operations-one-line layout overrides. Meant to be mounted
// at /oneline-layout behind the operations:read / operations:write gate (it's the
// operations surface's own display state, not a diagram):
//
//	GET    /overrides   list every node nudge
//	PUT    /overrides   save one node nudge {feature_class, feature_id, x, y}
//	DELETE /overrides   clear all node nudges (reset to the computed layout)
//	GET    /wires       list every hand-routed wire
//	PUT    /wires       save one wire's bends {wire_key, vertices:[[x,y]...]} (empty = reset that wire)
//	DELETE /wires       clear all hand-routed wires
func OverrideRoutes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/overrides", listOverridesHandler(db))
	r.Put("/overrides", putOverrideHandler(db))
	r.Delete("/overrides", clearOverridesHandler(db))
	r.Get("/wires", listWireOverridesHandler(db))
	r.Put("/wires", putWireOverrideHandler(db))
	r.Delete("/wires", clearWireOverridesHandler(db))
	return r
}

func listWireOverridesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := ListWireOverrides(r.Context(), db)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list wire overrides")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"wires": rows})
	}
}

type putWireOverrideRequest struct {
	WireKey  string      `json:"wire_key"`
	Vertices [][]float64 `json:"vertices"`
}

func putWireOverrideHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req putWireOverrideRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		req.WireKey = strings.TrimSpace(req.WireKey)
		if req.WireKey == "" {
			writeError(w, http.StatusBadRequest, "wire_key is required")
			return
		}
		for _, v := range req.Vertices {
			if len(v) != 2 {
				writeError(w, http.StatusBadRequest, "each vertex must be an [x, y] pair")
				return
			}
		}
		// No bends left -> the wire is back to its auto route; delete the row.
		if len(req.Vertices) == 0 {
			if err := DeleteWireOverride(r.Context(), db, req.WireKey); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to reset wire")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		raw, err := json.Marshal(req.Vertices)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid vertices")
			return
		}
		o := &WireOverride{WireKey: req.WireKey, Vertices: raw}
		if actor := auth.ActorID(r.Context()); actor != "" {
			o.UpdatedBy = &actor
		}
		if err := UpsertWireOverride(r.Context(), db, o); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save wire")
			return
		}
		writeJSON(w, http.StatusOK, o)
	}
}

func clearWireOverridesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ClearWireOverrides(r.Context(), db); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear wire overrides")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listOverridesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := ListOverrides(r.Context(), db)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list overrides")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"overrides": rows})
	}
}

type putOverrideRequest struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
}

func putOverrideHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req putOverrideRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		req.FeatureClass = strings.TrimSpace(req.FeatureClass)
		req.FeatureID = strings.TrimSpace(req.FeatureID)
		if req.FeatureClass == "" || req.FeatureID == "" {
			writeError(w, http.StatusBadRequest, "feature_class and feature_id are required")
			return
		}
		o := &OnelineOverride{FeatureClass: req.FeatureClass, FeatureID: req.FeatureID, X: req.X, Y: req.Y}
		if actor := auth.ActorID(r.Context()); actor != "" {
			o.UpdatedBy = &actor
		}
		if err := UpsertOverride(r.Context(), db, o); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save override")
			return
		}
		writeJSON(w, http.StatusOK, o)
	}
}

func clearOverridesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ClearOverrides(r.Context(), db); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear overrides")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
