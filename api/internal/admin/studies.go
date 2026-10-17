package admin

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/studies"
	"github.com/uptrace/bun"
)

// studyRow is one analysis study as the admin screen sees it for a given class:
// its identity, whether its role makes it APPLICABLE at all (the code floor), and
// whether an admin has DISABLED it. The UI offers a toggle only for applicable
// studies; an inapplicable one is shown greyed (or hidden) and can't be enabled.
type studyRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Applicable bool   `json:"applicable"`
	Disabled   bool   `json:"disabled"`
}

// analysisStudiesHandler returns every registered study with its applicable /
// disabled state for the class -- enough for the admin checklist to render the
// whole suite, enable the applicable ones, and grey the rest.
func analysisStudiesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		fc := new(metadata.FeatureClass)
		if err := db.NewSelect().Model(fc).Where("code = ?", code).Scan(r.Context()); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, notFound("feature class %q does not exist", code))
				return
			}
			writeError(w, err)
			return
		}
		disabled, err := studies.LoadDisabled(r.Context(), db, fc.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		rows := make([]studyRow, 0, len(studies.Registry))
		for _, s := range studies.Registry {
			rows = append(rows, studyRow{
				ID:         s.ID,
				Name:       s.Name,
				Category:   s.Category,
				Applicable: s.Applies(fc),
				Disabled:   disabled[s.ID],
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"studies": rows})
	}
}

// setAnalysisStudiesHandler replaces the pruned-study set for a class. The body
// is {"disabled_study_ids": [...]}; only known, applicable ids are stored (see
// studies.SetDisabled), so an empty list re-enables every applicable study.
func setAnalysisStudiesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var body struct {
			DisabledStudyIDs []string `json:"disabled_study_ids"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		fc := new(metadata.FeatureClass)
		if err := db.NewSelect().Model(fc).Where("code = ?", code).Scan(r.Context()); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, notFound("feature class %q does not exist", code))
				return
			}
			writeError(w, err)
			return
		}
		applicable := func(id string) bool { return studies.AppliesTo(id, fc) }
		if err := studies.SetDisabled(r.Context(), db, fc.ID, applicable, body.DisabledStudyIDs); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
