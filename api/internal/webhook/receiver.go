package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// Public request headers the receiver understands.
const (
	signatureHeader   = "X-VoltEdge-Signature"
	timestampHeader   = "X-VoltEdge-Timestamp"
	idempotencyHeader = "Idempotency-Key"

	// replayWindow bounds how far a signed request's timestamp may drift from
	// server time. A captured-and-replayed push older than this is rejected even
	// with a valid signature.
	replayWindow = 5 * time.Minute
)

// ReceiverRoutes is the PUBLIC ingestion surface, mounted unauthenticated at
// /webhooks (the RBAC middleware stack does NOT wrap it -- each endpoint carries
// its own per-endpoint credential). The caller is expected to wrap this in a
// rate limiter and a body-size cap at the mount site.
//
//	POST /{id}   receive a push for endpoint {id}
//
// The receiver only authenticates and enqueues; the worker applies. It answers
// fast (202) and never blocks the sender on the actual apply.
func ReceiverRoutes(db *bun.DB, cipher *secretbox.Cipher) chi.Router {
	rec := &receiver{db: db, cipher: cipher}
	r := chi.NewRouter()
	r.Post("/{id}", rec.receive)
	return r
}

type receiver struct {
	db     *bun.DB
	cipher *secretbox.Cipher
}

func (rec *receiver) receive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	// Load the endpoint. Any reason it can't receive -- missing, inactive, or a
	// pull-only endpoint -- collapses to one indistinguishable 404 so the surface
	// isn't an existence oracle.
	ep := new(Endpoint)
	if err := rec.db.NewSelect().Model(ep).Where("id = ?", id).Scan(ctx); err != nil {
		writeError(w, notFound("no such webhook endpoint"))
		return
	}
	if !ep.Active || ep.Direction != "push" {
		writeError(w, notFound("no such webhook endpoint"))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, invalid("could not read request body: %v", err))
		return
	}

	if err := rec.authenticate(ctx, ep, r, body); err != nil {
		writeError(w, err)
		return
	}

	var idempotencyKey *string
	if k := strings.TrimSpace(r.Header.Get(idempotencyHeader)); k != "" {
		idempotencyKey = &k
	}

	deliveryID, deduped, err := enqueueDelivery(ctx, rec.db, ep.ID, "push", idempotencyKey, body, captureHeaders(r))
	if err != nil {
		writeError(w, err)
		return
	}

	// 202: accepted for async apply. A deduped replay reports the original id at
	// 200 -- the sender's retry is acknowledged as already-received, not redone.
	status := http.StatusAccepted
	if deduped {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"delivery_id": deliveryID, "deduped": deduped})
}

// authenticate enforces the endpoint's auth_mode against the presented request.
// For "both", the sender picks a scheme by which credential it presents: a
// signature header routes to HMAC, otherwise bearer.
func (rec *receiver) authenticate(ctx context.Context, ep *Endpoint, r *http.Request, body []byte) error {
	switch ep.AuthMode {
	case "hmac":
		return rec.verifyHMAC(ep, r, body)
	case "bearer":
		return rec.verifyBearer(ctx, ep, r)
	case "both":
		if r.Header.Get(signatureHeader) != "" {
			return rec.verifyHMAC(ep, r, body)
		}
		return rec.verifyBearer(ctx, ep, r)
	default:
		return unauthorized("endpoint has no usable auth mode")
	}
}

// verifyHMAC checks X-VoltEdge-Signature = hex(HMAC-SHA256(secret, ts+"."+body))
// with X-VoltEdge-Timestamp inside the replay window. Every failure returns the
// same vague 401 so it can't distinguish "bad timestamp" from "bad signature".
func (rec *receiver) verifyHMAC(ep *Endpoint, r *http.Request, body []byte) error {
	if rec.cipher == nil || len(ep.SigningSecretEnc) == 0 {
		return unauthorized("signature verification unavailable")
	}
	secret, err := rec.cipher.Decrypt(ep.SigningSecretEnc)
	if err != nil {
		return unauthorized("signature verification unavailable")
	}

	tsRaw := strings.TrimSpace(r.Header.Get(timestampHeader))
	if tsRaw == "" {
		return unauthorized("invalid signature")
	}
	tsSecs, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return unauthorized("invalid signature")
	}
	if drift := math.Abs(time.Since(time.Unix(tsSecs, 0)).Seconds()); drift > replayWindow.Seconds() {
		return unauthorized("invalid signature")
	}

	provided := strings.TrimPrefix(strings.TrimSpace(r.Header.Get(signatureHeader)), "sha256=")
	providedMAC, err := hex.DecodeString(provided)
	if err != nil {
		return unauthorized("invalid signature")
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(tsRaw))
	mac.Write([]byte("."))
	mac.Write(body)
	if !hmac.Equal(providedMAC, mac.Sum(nil)) {
		return unauthorized("invalid signature")
	}
	return nil
}

// verifyBearer accepts an Authorization: Bearer <token> whose resolved user IS
// the endpoint's configured service user -- so a valid token for a different
// principal can't drive this endpoint.
func (rec *receiver) verifyBearer(ctx context.Context, ep *Endpoint, r *http.Request) error {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(authz, prefix) {
		return unauthorized("missing bearer token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, prefix))

	su, err := auth.ResolveAPIToken(ctx, rec.db, token)
	if err != nil || su == nil {
		return unauthorized("invalid bearer token")
	}
	if su.ID != ep.ServiceUserID {
		return unauthorized("token does not belong to this endpoint's service user")
	}
	return nil
}

// captureHeaders snapshots a safe, non-secret subset of the request headers for
// the delivery's audit record. Authorization and the raw signature are never
// stored -- only that a signature was present.
func captureHeaders(r *http.Request) json.RawMessage {
	h := map[string]any{
		"content_type":      r.Header.Get("Content-Type"),
		"user_agent":        r.Header.Get("User-Agent"),
		"timestamp":         r.Header.Get(timestampHeader),
		"idempotency_key":   r.Header.Get(idempotencyHeader),
		"signature_present": r.Header.Get(signatureHeader) != "",
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil
	}
	return b
}
