package webhook

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// deliveryListLimit caps the admin delivery log page (newest first).
const deliveryListLimit = 100

// AdminRoutes is the management surface for webhook endpoints and their delivery
// log, meant to be mounted at /webhook-endpoints behind the
// metadata:read/metadata:write gate (the same admin capability as external
// systems and domains; the frontend surfaces it under /admin). Unlike the public
// receiver, everything here is authenticated by the normal session/token stack.
//
//	GET    /                                     list endpoints (never secrets)
//	POST   /                                     create an endpoint
//	GET    /{id}                                 fetch one endpoint
//	PATCH  /{id}                                 update an endpoint
//	DELETE /{id}                                 remove an endpoint (cascades log)
//	GET    /{id}/deliveries                      recent delivery log
//	POST   /{id}/test                            paste-and-apply a payload inline
//	POST   /{id}/deliveries/{deliveryID}/replay  re-enqueue a stored delivery
func AdminRoutes(db *bun.DB, cipher *secretbox.Cipher) chi.Router {
	h := &adminAPI{store: &store{db: db, cipher: cipher}}
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Get("/{id}/deliveries", h.listDeliveries)
	r.Post("/{id}/test", h.test)
	r.Post("/{id}/deliveries/{deliveryID}/replay", h.replay)
	return r
}

type adminAPI struct{ store *store }

func (h *adminAPI) list(w http.ResponseWriter, r *http.Request) {
	rows, err := listEndpoints(r.Context(), h.store)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": rows})
}

func (h *adminAPI) create(w http.ResponseWriter, r *http.Request) {
	var req createEndpointRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := createEndpoint(r.Context(), h.store, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *adminAPI) get(w http.ResponseWriter, r *http.Request) {
	ep, err := getEndpoint(r.Context(), h.store, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, formatEndpoint(ep))
}

func (h *adminAPI) update(w http.ResponseWriter, r *http.Request) {
	var req updateEndpointRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := updateEndpoint(r.Context(), h.store, chi.URLParam(r, "id"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *adminAPI) delete(w http.ResponseWriter, r *http.Request) {
	if err := deleteEndpoint(r.Context(), h.store, chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminAPI) listDeliveries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Confirm the endpoint exists first, so a bad id is 404 not empty-200.
	if _, err := getEndpoint(ctx, h.store, chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	rows, err := listDeliveries(ctx, h.store.db, chi.URLParam(r, "id"), deliveryListLimit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": toDeliveryResponses(rows)})
}

// test is the paste-and-apply tester: it records a source="manual" delivery and
// applies it INLINE (synchronously), returning the per-item result so an admin
// can validate an integration's payload against the live model. A failure
// dead-letters the delivery (attempts exhausted) rather than requeuing, so the
// background worker won't re-run a hand test.
func (h *adminAPI) test(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ep, err := getEndpoint(ctx, h.store, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	payload, err := readJSONBody(r)
	if err != nil {
		writeError(w, err)
		return
	}

	deliveryID, _, err := enqueueDelivery(ctx, h.store.db, ep.ID, "manual", nil, payload, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	d, err := getDelivery(ctx, h.store.db, ep.ID, deliveryID)
	if err != nil {
		writeError(w, err)
		return
	}

	result, applyErr := applyInline(ctx, h.store.db, d)
	if applyErr != nil {
		// The delivery row carries the failure; report it to the tester too.
		writeError(w, applyErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery_id": deliveryID, "result": result})
}

// replay re-enqueues a stored delivery's payload as a NEW source="replay"
// delivery for the async worker to apply -- the original row is left untouched
// as the audit record. Idempotency keys are intentionally dropped so a replay is
// never deduped against the original.
func (h *adminAPI) replay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ep, err := getEndpoint(ctx, h.store, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	orig, err := getDelivery(ctx, h.store.db, ep.ID, chi.URLParam(r, "deliveryID"))
	if err != nil {
		writeError(w, err)
		return
	}
	deliveryID, _, err := enqueueDelivery(ctx, h.store.db, ep.ID, "replay", nil, orig.Payload, orig.Headers)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"delivery_id": deliveryID})
}

// --- shared HTTP helpers (also used by receiver.go) ---

func toDeliveryResponses(rows []Delivery) []deliveryResponse {
	out := make([]deliveryResponse, 0, len(rows))
	for i := range rows {
		d := &rows[i]
		out = append(out, deliveryResponse{
			ID:             d.ID,
			EndpointID:     d.EndpointID,
			Status:         d.Status,
			Source:         d.Source,
			IdempotencyKey: d.IdempotencyKey,
			Payload:        d.Payload,
			Attempts:       d.Attempts,
			Result:         d.Result,
			Error:          d.Error,
			CreatedAt:      d.CreatedAt,
			FinishedAt:     d.FinishedAt,
		})
	}
	return out
}

// readJSONBody reads a request body and validates it is syntactically JSON,
// returning it raw (the tester passes arbitrary native payloads straight to the
// adapter). Empty bodies are rejected with a clean 400.
func readJSONBody(r *http.Request) (json.RawMessage, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, invalid("could not read request body: %v", err)
	}
	if !json.Valid(raw) {
		return nil, invalid("request body is not valid JSON")
	}
	return raw, nil
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
	case isAuthError(err):
		status = http.StatusUnauthorized
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
