package export

// HTTP surface for CIM sync targets: CRUD (metadata:write) plus a run action that
// enqueues an incremental CIM export. Mounted under /export by Routes.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

type syncTargetView struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Description          *string   `json:"description,omitempty"`
	Active               bool      `json:"active"`
	FeatureClasses       []string  `json:"feature_classes"`
	Bbox                 []float64 `json:"bbox,omitempty"`
	TerritoryIDs         []string  `json:"territory_ids,omitempty"`
	CimProfile           *string   `json:"cim_profile,omitempty"`
	TargetSRID           *int      `json:"target_srid,omitempty"`
	Schedule             *string   `json:"schedule,omitempty"`
	HasBaseline          bool      `json:"has_baseline"`
	BaselineModelMRID    *string   `json:"baseline_model_mrid,omitempty"`
	BaselineFeatureCount *int      `json:"baseline_feature_count,omitempty"`
	LastExportedAt       *string   `json:"last_exported_at,omitempty"`
	LastJobID            *string   `json:"last_job_id,omitempty"`
}

func toSyncTargetView(t *SyncTarget) syncTargetView {
	v := syncTargetView{
		ID:                   t.ID,
		Name:                 t.Name,
		Description:          t.Description,
		Active:               t.Active,
		FeatureClasses:       t.FeatureClasses,
		Bbox:                 t.Bbox,
		TerritoryIDs:         t.TerritoryIDs,
		CimProfile:           t.CimProfile,
		TargetSRID:           t.TargetSRID,
		Schedule:             t.Schedule,
		HasBaseline:          t.BaselineObjectKey != nil,
		BaselineModelMRID:    t.BaselineModelMRID,
		BaselineFeatureCount: t.BaselineFeatureCount,
		LastJobID:            t.LastJobID,
	}
	if t.LastExportedAt != nil {
		s := t.LastExportedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		v.LastExportedAt = &s
	}
	return v
}

// syncTargetInput is the create/update body. Scope fields define what the target
// exports; the baseline state is server-managed and never sent.
type syncTargetInput struct {
	Name           string    `json:"name"`
	Description    *string   `json:"description"`
	Active         *bool     `json:"active"`
	FeatureClasses []string  `json:"feature_classes"`
	Bbox           []float64 `json:"bbox"`
	TerritoryIDs   []string  `json:"territory_ids"`
	CimProfile     *string   `json:"cim_profile"`
	TargetSRID     *int      `json:"target_srid"`
	Schedule       *string   `json:"schedule"`
}

// normalizeSchedule keeps only a valid cadence token (hourly|daily|weekly); any
// other value (including "manual"/"" ) stores NULL = manual.
func normalizeSchedule(s *string) *string {
	if s == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(*s)) {
	case "hourly", "daily", "weekly":
		v := strings.ToLower(strings.TrimSpace(*s))
		return &v
	default:
		return nil
	}
}

func (in *syncTargetInput) validate() string {
	if strings.TrimSpace(in.Name) == "" {
		return "name is required"
	}
	if len(in.FeatureClasses) == 0 {
		return "at least one feature class is required"
	}
	if len(in.Bbox) != 0 && len(in.Bbox) != 4 {
		return "bbox must be [minX, minY, maxX, maxY] or omitted"
	}
	return ""
}

func listSyncTargetsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targets, err := listSyncTargets(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]syncTargetView, len(targets))
		for i := range targets {
			views[i] = toSyncTargetView(&targets[i])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"sync_targets": views})
	}
}

func getSyncTargetHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, err := getSyncTarget(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, "sync target not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toSyncTargetView(t))
	}
}

func createSyncTargetHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in syncTargetInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if msg := in.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		t := &SyncTarget{
			Name:           strings.TrimSpace(in.Name),
			Description:    in.Description,
			Active:         in.Active == nil || *in.Active,
			FeatureClasses: in.FeatureClasses,
			Bbox:           in.Bbox,
			TerritoryIDs:   in.TerritoryIDs,
			CimProfile:     in.CimProfile,
			TargetSRID:     in.TargetSRID,
			Schedule:       normalizeSchedule(in.Schedule),
		}
		if user, ok := auth.UserFrom(r.Context()); ok {
			t.CreatedBy = &user.ID
		}
		if _, err := db.NewInsert().Model(t).Returning("*").Exec(r.Context()); err != nil {
			writeErr(w, http.StatusBadRequest, friendlyInsertErr(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(toSyncTargetView(t))
	}
}

func updateSyncTargetHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, err := getSyncTarget(r.Context(), db, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "sync target not found")
			return
		}
		var in syncTargetInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if msg := in.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}

		// Model-based update so the array / jsonb column tags encode correctly.
		upd := &SyncTarget{
			ID:             id,
			Name:           strings.TrimSpace(in.Name),
			Description:    in.Description,
			Active:         in.Active == nil || *in.Active,
			FeatureClasses: in.FeatureClasses,
			Bbox:           in.Bbox,
			TerritoryIDs:   in.TerritoryIDs,
			CimProfile:     in.CimProfile,
			TargetSRID:     in.TargetSRID,
			Schedule:       normalizeSchedule(in.Schedule),
			UpdatedAt:      time.Now(),
		}
		cols := []string{"name", "description", "active", "feature_classes", "bbox", "territory_ids", "cim_profile", "target_srid", "schedule", "updated_at"}

		// Editing the SCOPE invalidates the baseline -- a diff against a model built
		// over a different scope would be nonsense, so the next run is a fresh full
		// load. Clear the baseline pointer (the old object is left for cleanup).
		if scopeChanged(existing, &in) {
			cols = append(cols, "baseline_object_key", "baseline_model_mrid", "baseline_feature_count", "last_exported_at", "last_job_id")
		}
		if _, err := db.NewUpdate().Model(upd).Column(cols...).WherePK().Exec(r.Context()); err != nil {
			writeErr(w, http.StatusBadRequest, friendlyInsertErr(err))
			return
		}
		updated, _ := getSyncTarget(r.Context(), db, id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toSyncTargetView(updated))
	}
}

func deleteSyncTargetHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, err := getSyncTarget(r.Context(), db, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "sync target not found")
			return
		}
		if _, err := db.NewDelete().Model((*SyncTarget)(nil)).Where("id = ?", id).Exec(r.Context()); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Best-effort: drop the baseline object so it doesn't orphan in the bucket.
		if existing.BaselineObjectKey != nil && baselineStore != nil {
			_ = baselineStore.Delete(r.Context(), *existing.BaselineObjectKey)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// runSyncTargetHandler enqueues an incremental CIM export for a target.
func runSyncTargetHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "no session")
			return
		}
		t, err := getSyncTarget(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, "sync target not found")
			return
		}
		if !t.Active {
			writeErr(w, http.StatusConflict, "sync target is inactive")
			return
		}
		id, err := enqueueExport(r.Context(), db, user.ID, scopeForTarget(t))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"job_id": id})
	}
}

// --- helpers -------------------------------------------------------------

func scopeChanged(existing *SyncTarget, in *syncTargetInput) bool {
	if !equalStrs(existing.FeatureClasses, in.FeatureClasses) ||
		!equalStrs(existing.TerritoryIDs, in.TerritoryIDs) ||
		!equalFloats(existing.Bbox, in.Bbox) ||
		!equalStrPtr(existing.CimProfile, in.CimProfile) ||
		!equalIntPtr(existing.TargetSRID, in.TargetSRID) {
		return true
	}
	return false
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func friendlyInsertErr(err error) string {
	if strings.Contains(err.Error(), "cim_sync_targets_name_key") || strings.Contains(err.Error(), "duplicate key") {
		return "a sync target with that name already exists"
	}
	return err.Error()
}
