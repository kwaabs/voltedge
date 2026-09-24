package servicelayers

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/outbound"
)

// maxDiscoverBytes caps the capabilities document we relay. These are text
// (GetCapabilities XML or Esri ?f=json) and rarely large, but a hostile or
// misconfigured service shouldn't be able to make us buffer an unbounded body.
const maxDiscoverBytes = 8 << 20 // 8 MiB

// discoverRequest asks the server to fetch a capabilities document on the admin
// form's behalf so a new/edited layer's fields can be auto-filled. URL is the
// full GetCapabilities / ?f=json URL the browser built. Auth is the inline
// credential typed into the form (create, or an edit that re-typed the secret);
// when it's absent and ID names an existing layer, that layer's stored credential
// is used instead.
type discoverRequest struct {
	URL  string        `json:"url"`
	ID   string        `json:"id"`
	Auth *discoverAuth `json:"auth"`
}

type discoverAuth struct {
	Type       string `json:"type"`
	Header     string `json:"header"`
	QueryParam string `json:"query_param"`
	Username   string `json:"username"`
	Secret     string `json:"secret"`
}

// discoverHandler performs a one-shot, SSRF-vetted GET of a capabilities document
// (WMS/WMTS/WFS GetCapabilities XML, or an Esri MapServer/FeatureServer ?f=json)
// and returns it as a JSON string for the browser to parse (OpenLayers'
// WMS/WMTS capabilities readers). It's a mutating route -- so it rides the mount's
// metadata:write gate -- because it reaches an arbitrary operator-supplied URL;
// the SSRF gate in api/internal/outbound blocks internal/loopback targets.
func discoverHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req discoverRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		target, err := outbound.PublicHTTPURL(req.URL)
		if err != nil {
			writeError(w, invalid("url is not a usable http(s) url: %v", err))
			return
		}
		authz, err := s.discoverAuth(r, req, target)
		if err != nil {
			writeError(w, err)
			return
		}

		hr, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
		if err != nil {
			writeError(w, upstream("could not build the capabilities request: %v", err))
			return
		}
		hr.Header.Set("Accept", "application/xml, text/xml, application/json;q=0.9, */*;q=0.5")
		if err := authz.Authorize(hr); err != nil {
			writeError(w, invalid("auth is misconfigured: %v", err))
			return
		}

		resp, err := outbound.SafeClient(proxyTimeout).Do(hr)
		if err != nil {
			writeError(w, upstream("could not reach the service: %v", err))
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoverBytes))
		if err != nil {
			writeError(w, upstream("could not read the capabilities response: %v", err))
			return
		}
		if resp.StatusCode >= 400 {
			writeError(w, upstream("service returned HTTP %d for the capabilities request", resp.StatusCode))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"content_type": resp.Header.Get("Content-Type"),
			"body":         string(body),
		})
	}
}

// discoverAuth resolves the credential for a discovery fetch. An inline secret
// typed into the form wins. Otherwise, when an existing layer id is given, that
// layer's stored credential is used -- but only when the discovery target is on
// the layer's own host, so a saved credential can never be aimed at an arbitrary
// third party.
func (s *store) discoverAuth(r *http.Request, req discoverRequest, target *url.URL) (outbound.Auth, error) {
	if req.Auth != nil && req.Auth.Type != "" && req.Auth.Type != "none" {
		return outbound.Auth{
			Type:       req.Auth.Type,
			Header:     req.Auth.Header,
			QueryParam: req.Auth.QueryParam,
			Username:   req.Auth.Username,
			Secret:     req.Auth.Secret,
		}, nil
	}
	if req.ID != "" {
		sl, err := getRow(r.Context(), s, req.ID)
		if err != nil {
			return outbound.Auth{}, err
		}
		if sl.AuthType != "" && sl.AuthType != "none" {
			lu, err := outbound.PublicHTTPURL(sl.URL)
			if err != nil {
				return outbound.Auth{}, invalid("saved layer url is not usable: %v", err)
			}
			if !strings.EqualFold(lu.Host, target.Host) {
				return outbound.Auth{}, invalid("discovery target host %q does not match the saved layer host %q", target.Host, lu.Host)
			}
			return s.authFor(sl)
		}
	}
	return outbound.Auth{Type: "none"}, nil
}
