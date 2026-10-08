package webhookout

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/outbound"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

// sendTimeout bounds one POST (connect + write + read). A subscriber that needs
// longer than this is treated as a transient failure and retried.
const sendTimeout = 30 * time.Second

// maxResponseBytes caps how much of a subscriber's response body we read before
// discarding it -- we only care about the status code, not the body.
const maxResponseBytes = 64 << 10

// sendDelivery POSTs one delivery's envelope to its subscription's target_url,
// signed with the subscription's HMAC secret, and returns the subscriber's HTTP
// status plus an error classifying the outcome:
//   - nil error                    -> 2xx, delivered.
//   - permanentError               -> will never succeed (bad URL, no/undecryptable
//     secret, a 4xx rejection): the worker dead-letters immediately.
//   - any other (transient) error  -> a 5xx/408/429 or a network failure: the
//     worker backs off and retries until the attempt cap.
func sendDelivery(ctx context.Context, db bun.IDB, cipher *secretbox.Cipher, d *OutboundDelivery) (int, error) {
	sub, err := getSubscription(ctx, db, d.SubscriptionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, permanentf("subscription %s no longer exists", d.SubscriptionID)
		}
		return 0, fmt.Errorf("webhookout: loading subscription %s: %w", d.SubscriptionID, err)
	}

	cfg, err := sub.parseConfig()
	if err != nil {
		return 0, permanentf("subscription config invalid: %v", err)
	}
	// Validate the target up front: a non-public / malformed URL can never work,
	// and SafeClient would refuse it at connect time anyway.
	target, err := outbound.PublicHTTPURL(cfg.TargetURL)
	if err != nil {
		return 0, permanentf("target_url is not a valid public URL: %v", err)
	}

	// The body is signed with the subscription's HMAC secret, so the receiver can
	// trust it. No secret (or no cipher to unseal it) is a config error, not a
	// transient one -- dead-letter rather than retry an unsignable delivery.
	if cipher == nil {
		return 0, permanentf("outbound signing unavailable: %s is not set", secretbox.EnvKey)
	}
	if len(sub.SigningSecretEnc) == 0 {
		return 0, permanentf("subscription has no signing secret configured")
	}
	secret, err := cipher.Decrypt(sub.SigningSecretEnc)
	if err != nil {
		return 0, permanentf("subscription signing secret could not be decrypted: %v", err)
	}

	sig, ts := signBody(secret, d.Payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(d.Payload))
	if err != nil {
		return 0, permanentf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(eventHeader, d.EventType)
	req.Header.Set(deliveryHeader, d.ID)
	req.Header.Set(timestampHeader, ts)
	req.Header.Set(signatureHeader, sig)

	resp, err := outbound.SafeClient(sendTimeout).Do(req)
	if err != nil {
		// Network failure or an SSRF-block at connect -- transient (a tunnel coming
		// up, DNS settling); the attempt cap bounds how long we keep trying.
		return 0, fmt.Errorf("posting to subscriber: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return resp.StatusCode, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests:
		// A 4xx (other than 408/429) is the subscriber rejecting this request on
		// its merits -- retrying sends the identical bytes to the same no. Permanent.
		return resp.StatusCode, permanentf("subscriber rejected delivery: HTTP %d", resp.StatusCode)
	default:
		// 5xx, 408, 429 -- the subscriber is briefly unable; back off and retry.
		return resp.StatusCode, fmt.Errorf("subscriber returned HTTP %d", resp.StatusCode)
	}
}
