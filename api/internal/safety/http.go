package safety

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the operational-safety endpoints:
//
//	GET  /safety/tags                     active/all safety tags (?device_class=&device_id=&clearance_id=&active=)
//	POST /safety/tags                     place a tag on a device
//	POST /safety/tags/{id}/remove         clear a tag
//	GET  /safety/grounds                  applied grounds (?feature_class=&feature_id=&clearance_id=&active=)
//	POST /safety/grounds                  apply a safety earth
//	POST /safety/grounds/{id}/remove      remove a safety earth
//	GET  /safety/clearances               clearances board (?status=&incident_id=&open=true)
//	POST /safety/clearances               issue a permit-to-work
//	GET  /safety/clearances/{id}          one clearance with its tags & grounds
//	POST /safety/clearances/{id}/surrender  hand back + release its tags/grounds
//	POST /safety/clearances/{id}/cancel   void + release its tags/grounds
//
// GET reads (safety:read), POST writes (safety:write); the method-aware gate
// RequirePermission(safety:read, safety:write) is applied on the mount in
// api/cmd/server/main.go -- this router carries no auth of its own, the same
// arrangement oms / operations / faults / wam use.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()

	r.Get("/tags", listTagsHandler(db))
	r.Post("/tags", placeTagHandler(db))
	r.Post("/tags/{id}/remove", removeTagHandler(db))

	r.Get("/grounds", listGroundsHandler(db))
	r.Post("/grounds", applyGroundHandler(db))
	r.Post("/grounds/{id}/remove", removeGroundHandler(db))

	r.Get("/clearances", listClearancesHandler(db))
	r.Post("/clearances", createClearanceHandler(db))
	r.Get("/clearances/{id}", getClearanceHandler(db))
	r.Post("/clearances/{id}/surrender", clearanceActionHandler(db, SurrenderClearance))
	r.Post("/clearances/{id}/cancel", clearanceActionHandler(db, CancelClearance))

	return r
}

// --- tags ---

func listTagsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tags, err := ListTags(r.Context(), db, TagFilter{
			DeviceClass: q.Get("device_class"),
			DeviceID:    q.Get("device_id"),
			ClearanceID: q.Get("clearance_id"),
			ActiveOnly:  q.Get("active") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
	}
}

type placeTagRequest struct {
	DeviceClass     string  `json:"device_class"`
	DeviceID        string  `json:"device_id"`
	TagType         string  `json:"tag_type"`
	BlocksOperation *bool   `json:"blocks_operation"`
	Reason          *string `json:"reason"`
	ClearanceID     *string `json:"clearance_id"`
	Notes           *string `json:"notes"`
}

func placeTagHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req placeTagRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		tag, err := PlaceTag(r.Context(), db, PlaceTagInput{
			DeviceClass:     req.DeviceClass,
			DeviceID:        req.DeviceID,
			TagType:         req.TagType,
			BlocksOperation: req.BlocksOperation,
			Reason:          req.Reason,
			ClearanceID:     req.ClearanceID,
			Notes:           req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, tag)
	}
}

func removeTagHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag, err := RemoveTag(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tag)
	}
}

// --- grounds ---

func listGroundsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		grounds, err := ListGrounds(r.Context(), db, GroundFilter{
			FeatureClass: q.Get("feature_class"),
			FeatureID:    q.Get("feature_id"),
			ClearanceID:  q.Get("clearance_id"),
			ActiveOnly:   q.Get("active") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"grounds": grounds})
	}
}

type applyGroundRequest struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	Reason       *string `json:"reason"`
	ClearanceID  *string `json:"clearance_id"`
	Notes        *string `json:"notes"`
}

func applyGroundHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req applyGroundRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		g, err := ApplyGround(r.Context(), db, ApplyGroundInput{
			FeatureClass: req.FeatureClass,
			FeatureID:    req.FeatureID,
			Reason:       req.Reason,
			ClearanceID:  req.ClearanceID,
			Notes:        req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, g)
	}
}

func removeGroundHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, err := RemoveGround(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, g)
	}
}

// --- clearances ---

func listClearancesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		rows, err := ListClearances(r.Context(), db, ClearanceFilter{
			Status:     q.Get("status"),
			IncidentID: q.Get("incident_id"),
			OpenOnly:   q.Get("open") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"clearances": rows})
	}
}

type createClearanceRequest struct {
	Reference        string  `json:"reference"`
	IncidentID       *string `json:"incident_id"`
	SwitchingOrderID *string `json:"switching_order_id"`
	WorkDescription  *string `json:"work_description"`
	IssuedTo         *string `json:"issued_to"`
	Notes            *string `json:"notes"`
}

func createClearanceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createClearanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		c, err := CreateClearance(r.Context(), db, CreateClearanceInput{
			Reference:        req.Reference,
			IncidentID:       req.IncidentID,
			SwitchingOrderID: req.SwitchingOrderID,
			WorkDescription:  req.WorkDescription,
			IssuedTo:         req.IssuedTo,
			Notes:            req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, c)
	}
}

func getClearanceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := GetClearance(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		if c == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such clearance"})
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

// clearanceActionHandler adapts the surrender/cancel lifecycle functions (both
// *bun.DB, both release the clearance's tags/grounds) to a chi handler.
func clearanceActionHandler(db *bun.DB, fn func(context.Context, *bun.DB, string) (*Clearance, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := fn(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}
