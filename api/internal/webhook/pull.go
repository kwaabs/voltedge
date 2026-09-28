package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/outbound"
)

const (
	// pullTimeout bounds the whole outbound fetch (connect + read).
	pullTimeout = 30 * time.Second
	// pullMaxBodyBytes caps a fetched payload so a hostile or misconfigured
	// upstream can't stream an unbounded body into memory. The delivery is applied
	// inline like the tester, so this is a single admin-triggered call.
	pullMaxBodyBytes = 8 << 20 // 8 MiB
)

// pullFetch is the user-triggered pull: it fetches a pull endpoint's configured
// URL through the SSRF-hardened outbound client, records the response as a
// source="pull" delivery, and applies it INLINE (like the tester) so the admin
// who pressed "Pull now" sees the outcome immediately. Pull is a manual action by
// design -- there is no scheduler; an admin (or the agent tool) invokes it.
//
// It mirrors handler.test's shape (enqueue -> get -> applyInline) but sources the
// payload from an outbound GET rather than the request body. A failure
// dead-letters the delivery (never requeued), the same terminal outcome as the
// tester, and surfaces as the returned error.
func pullFetch(ctx context.Context, s *store, endpointID string) (string, json.RawMessage, error) {
	ep, err := getEndpoint(ctx, s, endpointID)
	if err != nil {
		return "", nil, err
	}
	if ep.Direction != "pull" {
		return "", nil, invalid("endpoint %q is a push endpoint; only a pull endpoint can fetch", ep.ID)
	}
	if !ep.Active {
		return "", nil, invalid("endpoint %q is inactive", ep.ID)
	}

	var cfg pullConfig
	if len(ep.Config) > 0 {
		if err := json.Unmarshal(ep.Config, &cfg); err != nil {
			return "", nil, invalid("endpoint config is not valid JSON: %v", err)
		}
	}
	if strings.TrimSpace(cfg.PullURL) == "" {
		return "", nil, invalid("this pull endpoint has no config.pull_url to fetch")
	}
	// Reject a malformed or non-http(s) target up front with a clear message; the
	// SafeClient below still vets the resolved IP on connect and every redirect.
	if _, err := outbound.PublicHTTPURL(cfg.PullURL); err != nil {
		return "", nil, invalid("config.pull_url is not a valid public http(s) url: %v", err)
	}

	body, err := s.fetchPull(ctx, ep, cfg)
	if err != nil {
		return "", nil, err
	}

	// Record where the payload came from for the delivery audit row.
	headers, _ := json.Marshal(map[string]string{"pull_url": strings.TrimSpace(cfg.PullURL)})

	deliveryID, _, err := enqueueDelivery(ctx, s.db, ep.ID, "pull", nil, body, headers)
	if err != nil {
		return "", nil, err
	}
	d, err := getDelivery(ctx, s.db, ep.ID, deliveryID)
	if err != nil {
		return "", nil, err
	}
	result, err := applyInline(ctx, s.db, d)
	if err != nil {
		// The delivery row carries the failure; report it to the caller too.
		return deliveryID, nil, err
	}
	return deliveryID, result, nil
}

// fetchPull performs the vetted outbound GET and returns the response body as raw
// JSON, capped at pullMaxBodyBytes. Authentication (when configured) uses the
// endpoint's unsealed credential in whichever shape outbound.Auth injects.
func (s *store) fetchPull(ctx context.Context, ep *Endpoint, cfg pullConfig) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(cfg.PullURL), nil)
	if err != nil {
		return nil, invalid("could not build pull request: %v", err)
	}
	req.Header.Set("Accept", "application/json")

	auth := outbound.Auth{
		Type:       defaulted(cfg.PullAuth.Type, "none"),
		Header:     cfg.PullAuth.Header,
		QueryParam: cfg.PullAuth.QueryParam,
		Username:   cfg.PullAuth.Username,
	}
	if auth.Type != "none" {
		secret, err := s.unsealSecret(ep)
		if err != nil {
			return nil, err
		}
		if secret == "" {
			return nil, invalid("pull_auth.type is %q but this endpoint has no stored credential -- set the pull credential (signing_secret)", auth.Type)
		}
		auth.Secret = secret
	}
	if err := auth.Authorize(req); err != nil {
		return nil, invalid("pull auth: %v", err)
	}

	resp, err := outbound.SafeClient(pullTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhook: pull fetch %s: %w", strings.TrimSpace(cfg.PullURL), err)
	}
	defer resp.Body.Close()

	// Read one byte past the cap so an over-limit body is detectable.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, pullMaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("webhook: read pull response: %w", err)
	}
	if len(raw) > pullMaxBodyBytes {
		return nil, invalid("pull response exceeds the %d-byte limit", pullMaxBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, invalid("pull_url returned HTTP %d", resp.StatusCode)
	}
	if !json.Valid(raw) {
		return nil, invalid("pull_url did not return valid JSON")
	}
	return json.RawMessage(raw), nil
}
