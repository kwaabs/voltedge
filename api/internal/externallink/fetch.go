package externallink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/kwaabs/voltedge/api/internal/externalsystem"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// maxResponseBytes caps how much of an upstream response the proxy will
// read into memory before giving up -- a misbehaving or hostile system
// can't make us buffer an unbounded body.
const maxResponseBytes = 2 << 20 // 2 MiB

// fetchTimeout bounds the whole outbound request (connect + read).
const fetchTimeout = 10 * time.Second

// linkDataResponse is what GET /{id}/data returns: the normalized payload
// plus enough about the source system for the panel to caption the card.
type linkDataResponse struct {
	System    systemRef `json:"system"`
	Format    string    `json:"format"`
	FetchedAt time.Time `json:"fetched_at"`
	Data      any       `json:"data"`
}

type systemRef struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// fetchLinkData resolves an api link to live data: it loads the link and
// its catalogued system, builds an authenticated request against the
// system's base_url + the link's resource_path, fetches it through an
// SSRF-hardened client, and normalizes the body (JSON as-is, XML into a
// nested map) into a single JSON shape. The decrypted credential never
// leaves this function.
func fetchLinkData(ctx context.Context, db bun.IDB, cipher *secretbox.Cipher, id string) (*linkDataResponse, error) {
	var link ExternalLink
	if err := db.NewSelect().Model(&link).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("link %s not found", id)
		}
		return nil, fmt.Errorf("externallink: fetch: load link: %w", err)
	}
	if link.Kind != "api" {
		return nil, invalid("live data is only available for api links")
	}
	if link.SystemID == nil {
		return nil, invalid("this link's external system no longer exists")
	}

	sys, err := loadSystemByID(ctx, db, *link.SystemID)
	if err != nil {
		return nil, err
	}

	path := ""
	if link.ResourcePath != nil {
		path = *link.ResourcePath
	}
	target, err := resolveTarget(sys.BaseURL, path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("externallink: fetch: build request: %w", err)
	}
	req.Header.Set("Accept", acceptFor(sys.ResponseFormat))
	if err := applyAuth(req, sys, cipher); err != nil {
		return nil, err
	}

	resp, err := safeClient().Do(req)
	if err != nil {
		return nil, upstream("could not reach %s: %v", sys.Code, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, upstream("reading the response from %s failed: %v", sys.Code, err)
	}
	if len(body) > maxResponseBytes {
		return nil, upstream("the response from %s exceeded the %d-byte limit", sys.Code, maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstream("%s returned HTTP %d", sys.Code, resp.StatusCode)
	}

	data, err := normalizeBody(sys.ResponseFormat, body)
	if err != nil {
		return nil, err
	}

	return &linkDataResponse{
		System:    systemRef{Code: sys.Code, Name: sys.Name},
		Format:    sys.ResponseFormat,
		FetchedAt: time.Now().UTC(),
		Data:      data,
	}, nil
}

// resolveTarget joins a validated (http/https) base_url with a validated
// relative resource_path per RFC 3986. Because resource_path carries no
// scheme or host (validateResourcePath), the result is always on
// base_url's origin -- there is no way for it to repoint elsewhere. Note
// the RFC semantics: give base_url a trailing slash to treat it as a
// directory a relative path extends, otherwise a relative path replaces
// its last segment.
func resolveTarget(baseURL, resourcePath string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", invalid("system base_url is not valid: %v", err)
	}
	if resourcePath == "" {
		return base.String(), nil
	}
	ref, err := url.Parse(resourcePath)
	if err != nil {
		return "", invalid("resource_path is not valid: %v", err)
	}
	return base.ResolveReference(ref).String(), nil
}

func acceptFor(format string) string {
	if format == "xml" {
		return "application/xml, text/xml;q=0.9, */*;q=0.1"
	}
	return "application/json, */*;q=0.1"
}

// applyAuth attaches the system's credential to the outbound request,
// decrypting it only here. auth_type='none' needs nothing; every other
// type needs both the configured shape and a decryptable secret, so a
// nil cipher (no encryption key on this server) is a clear error rather
// than a silent unauthenticated call.
func applyAuth(req *http.Request, sys *externalsystem.ExternalSystem, cipher *secretbox.Cipher) error {
	if sys.AuthType == "none" {
		return nil
	}
	if len(sys.CredentialEnc) == 0 {
		return invalid("external system %s has no stored credential", sys.Code)
	}
	if cipher == nil {
		return invalid("cannot use %s: the server has no encryption key configured (set %s)", sys.Code, secretbox.EnvKey)
	}
	secretBytes, err := cipher.Decrypt(sys.CredentialEnc)
	if err != nil {
		return upstream("could not decrypt the credential for %s (was the encryption key rotated?)", sys.Code)
	}
	secret := string(secretBytes)

	switch sys.AuthType {
	case "api_key":
		if sys.AuthHeader == nil || strings.TrimSpace(*sys.AuthHeader) == "" {
			return invalid("external system %s is missing its auth header name", sys.Code)
		}
		req.Header.Set(strings.TrimSpace(*sys.AuthHeader), secret)
	case "basic":
		username := ""
		if sys.AuthUsername != nil {
			username = *sys.AuthUsername
		}
		req.SetBasicAuth(username, secret)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+secret)
	default:
		return invalid("external system %s has an unsupported auth_type %q", sys.Code, sys.AuthType)
	}
	return nil
}

// safeClient builds an HTTP client hardened against SSRF. Its dialer's
// Control hook runs for the initial connection and every redirect hop,
// after DNS resolution, and refuses any address in a private, loopback,
// or otherwise non-public range -- so neither an admin-set base_url nor a
// DNS-rebinding trick can make the server reach into its own network.
func safeClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: fetchTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("blocked: %q is not an IP address", host)
			}
			if isBlockedIP(ip) {
				return fmt.Errorf("blocked: %s is not a public address", host)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: fetchTimeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   fetchTimeout,
			ResponseHeaderTimeout: fetchTimeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			// On a cross-host redirect, drop request headers that must not leak to a
			// different origin. Go strips Authorization/Cookie on a host change but NOT
			// a custom credential header -- which is exactly the auth_header form
			// applyAuth sets -- so a redirecting/compromised upstream could otherwise
			// harvest the stored secret. Keep only a safe transport allowlist.
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				for name := range req.Header {
					if !safeRedirectHeaders[name] {
						req.Header.Del(name)
					}
				}
			}
			return nil
		},
	}
}

// safeRedirectHeaders is the allowlist of request headers kept across a cross-host
// redirect; every other header (notably the auth_header credential) is stripped.
// Keys are canonical (net/http stores them so).
var safeRedirectHeaders = map[string]bool{
	"Accept":          true,
	"Accept-Encoding": true,
	"Accept-Language": true,
	"User-Agent":      true,
}

// isBlockedIP reports whether an IP is one the proxy must not connect to:
// loopback, private (RFC 1918 / ULA), link-local, unspecified, or
// multicast. Only genuinely public unicast addresses are allowed out.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// normalizeBody turns an upstream body into a JSON-serializable value:
// JSON is parsed as-is; XML (legacy systems) is decoded into a nested
// map so the same panel data card renders either.
func normalizeBody(format string, body []byte) (any, error) {
	if format == "xml" {
		return xmlToMap(body)
	}
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, upstream("the response was not valid JSON: %v", err)
	}
	return data, nil
}
