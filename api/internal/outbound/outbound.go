// Package outbound holds the shared primitives for making server-to-server HTTP
// requests to admin-configured third-party endpoints safely: an SSRF-hardened
// client that refuses to connect to non-public addresses, and a small Auth value
// that injects a decrypted credential onto a request in whichever shape the
// upstream expects.
//
// It exists so the service-layer proxy (api/internal/servicelayers) and any
// future outbound caller share exactly one network-hardening gate rather than
// re-deriving it. Decryption stays in the caller: each domain package owns its
// credential storage and passes the already-decrypted secret in via Auth, so a
// plaintext secret never travels through this package's persistence layer -- it
// only ever touches a live request here.
package outbound

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// SafeClient builds an HTTP client hardened against SSRF. Its dialer's Control
// hook runs for the initial connection and every redirect hop, after DNS
// resolution, and refuses any address in a private, loopback, or otherwise
// non-public range -- so neither an admin-set URL nor a DNS-rebinding trick can
// make the server reach into its own network. timeout bounds the whole request
// (connect + read); pass a value sized to the payload (tiles/imagery want more
// than a small JSON call).
func SafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("blocked: %q is not an IP address", host)
			}
			if IsBlockedIP(ip) {
				return fmt.Errorf("blocked: %s is not a public address", host)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			// On a cross-host redirect, drop request headers that must not leak to a
			// different origin. Go's stdlib already strips Authorization/Cookie/
			// WWW-Authenticate on a host change, but NOT a custom credential header --
			// which is exactly the api_key header form Authorize sets (e.g.
			// X-Api-Key). Without this, a redirecting/compromised upstream could bounce
			// the request to an attacker-controlled public host and harvest the stored
			// third-party key. Keep only a safe transport allowlist.
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				stripToSafeHeaders(req.Header)
			}
			return nil
		},
	}
}

// safeRedirectHeaders is the allowlist of request headers preserved across a
// cross-host redirect; every other header (notably any custom credential header)
// is dropped by stripToSafeHeaders. Keys are canonical (net/http stores them so).
var safeRedirectHeaders = map[string]bool{
	"Accept":          true,
	"Accept-Encoding": true,
	"Accept-Language": true,
	"User-Agent":      true,
}

// stripToSafeHeaders removes every header from h that is not in
// safeRedirectHeaders -- used on a cross-host redirect so an outbound credential
// (or any app-specific header) can't follow the request to a different origin.
func stripToSafeHeaders(h http.Header) {
	for name := range h {
		if !safeRedirectHeaders[name] {
			h.Del(name)
		}
	}
}

// IsBlockedIP reports whether an IP is one an outbound request must not reach:
// loopback, private (RFC 1918 / ULA), link-local, unspecified, or multicast.
// Only genuinely public unicast addresses are allowed out.
func IsBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// Auth describes how to authenticate one outbound request. The secret is already
// decrypted by the caller; Type "none" (or empty) needs no secret. The shape
// mirrors the auth_type/auth_header/auth_query_param/auth_username columns the
// catalog packages store.
type Auth struct {
	Type       string // none, api_key, basic, bearer
	Header     string // api_key: header name (header form)
	QueryParam string // api_key: query-param name (query-string form)
	Username   string // basic: username
	Secret     string // decrypted secret (password / token / key)
}

// Authorize attaches the credential to req in the configured shape, mutating its
// headers or -- for the api_key query-param form -- its URL query. It is the only
// place the secret is written onto a live request. A non-'none' type with an
// empty secret is an error rather than a silent unauthenticated call.
func (a Auth) Authorize(req *http.Request) error {
	switch a.Type {
	case "", "none":
		return nil
	case "api_key":
		if a.Secret == "" {
			return errors.New("outbound: api_key auth has no secret")
		}
		switch {
		case strings.TrimSpace(a.Header) != "":
			req.Header.Set(strings.TrimSpace(a.Header), a.Secret)
		case strings.TrimSpace(a.QueryParam) != "":
			q := req.URL.Query()
			q.Set(strings.TrimSpace(a.QueryParam), a.Secret)
			req.URL.RawQuery = q.Encode()
		default:
			return errors.New("outbound: api_key auth needs a header or query-param name")
		}
	case "basic":
		req.SetBasicAuth(a.Username, a.Secret)
	case "bearer":
		if a.Secret == "" {
			return errors.New("outbound: bearer auth has no secret")
		}
		req.Header.Set("Authorization", "Bearer "+a.Secret)
	default:
		return fmt.Errorf("outbound: unsupported auth type %q", a.Type)
	}
	return nil
}

// PublicHTTPURL parses raw and requires an absolute http(s) URL with a host --
// the shape every admin-configured endpoint must have before SafeClient even
// gets a chance to vet the resolved IP. Callers use it to reject a malformed or
// non-http target with a clear message up front.
func PublicHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("outbound: url is not valid: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("outbound: url must be http(s)")
	}
	if u.Host == "" {
		return nil, errors.New("outbound: url must include a host")
	}
	return u, nil
}
