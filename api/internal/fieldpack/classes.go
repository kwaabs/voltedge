package fieldpack

import (
	"encoding/json"
	"net/http"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// featureClassView is one packable feature class over the wire -- just enough
// for the field tool to render a pick-list (and section it by category) instead
// of making the user type UPPERCASE codes by hand. The device sends the `code`
// back verbatim in the POST /packs feature_classes array.
type featureClassView struct {
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	GeometryType string  `json:"geometry_type"`
	NetworkRole  string  `json:"network_role"`
	Network      *string `json:"network,omitempty"`
	Category     *string `json:"category,omitempty"`
}

// featureClassesHandler serves the catalog of classes a pack can include: every
// active class that actually has geometry ("none" classes can't be packed, same
// rule validatePackClasses enforces at enqueue). Rides the same features:read
// gate as the rest of the read surface. The field tool calls this once when
// opening the "new pack" form to populate its class picker.
func featureClassesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		classes, err := metadata.ListFeatureClasses(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]featureClassView, 0, len(classes))
		for i := range classes {
			c := &classes[i]
			if c.GeometryType == "none" {
				continue // matches validatePackClasses: a geometry-less class can't be packed
			}
			views = append(views, featureClassView{
				Code:         c.Code,
				Name:         c.Name,
				GeometryType: c.GeometryType,
				NetworkRole:  c.NetworkRole,
				Network:      c.Network,
				Category:     c.Category,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"feature_classes": views})
	}
}
