package servicelayers

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kwaabs/voltedge/api/internal/outbound"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

// The proxy is the Slice 2 rendering path: the map's OpenLayers raster sources
// (XYZ / TileWMS / TileArcGISRest) point at these same-origin endpoints instead
// of the third-party service directly. Doing so (a) keeps the upstream URL and
// any credential entirely server-side -- the browser never sees them -- (b)
// solves CORS, since to the browser every tile is same-origin, and (c) lets us
// vet every outbound address against the SSRF gate in api/internal/outbound. The
// credential is decrypted only for the lifetime of one forwarded request.

const (
	// proxyTimeout bounds one forwarded request. Larger than the JSON-fetch
	// timeout in externallink because a WMS GetMap or a map tile can be a
	// meaningful image, not a small document.
	proxyTimeout = 20 * time.Second
	// maxProxyBytes caps how much of an upstream response we relay, streamed (not
	// buffered), so a hostile or misconfigured service can't make us pump an
	// unbounded body to the client. Generous enough for real imagery/tiles.
	maxProxyBytes = 32 << 20 // 32 MiB
)

// tileProxyHandler serves one {z}/{x}/{y} tile for an xyz/vector_tile layer by
// substituting the coordinates into the layer's stored url template and
// forwarding. The template already carries the placeholders (validated on
// write), so there is no per-request query to merge.
func tileProxyHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sl, err := loadEnabled(r, s)
		if err != nil {
			writeError(w, err)
			return
		}
		// z/x/y come from the router; they only ever replace their placeholders in
		// the admin-set template, never the host, so a crafted value can't repoint
		// the request. Keep them as-is (OL sends plain integers).
		target := sl.URL
		target = strings.ReplaceAll(target, "{z}", chi.URLParam(r, "z"))
		target = strings.ReplaceAll(target, "{x}", chi.URLParam(r, "x"))
		target = strings.ReplaceAll(target, "{y}", chi.URLParam(r, "y"))

		u, err := outbound.PublicHTTPURL(target)
		if err != nil {
			writeError(w, invalid("layer url is not a usable http(s) url: %v", err))
			return
		}
		forward(w, r, s, sl, u)
	}
}

// genericProxyHandler forwards a KVP/REST request -- a WMS GetMap, a WMTS GetTile
// (KVP), or an Esri MapServer export -- to the layer's endpoint. OpenLayers'
// TileWMS/WMTS build the query string themselves and append it; TileArcGISRest
// appends an /export path segment. So the optional wildcard suffix is joined onto
// the endpoint's path and the request query is merged over the endpoint's own
// query, then auth is injected.
func genericProxyHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sl, err := loadEnabled(r, s)
		if err != nil {
			writeError(w, err)
			return
		}
		u, err := outbound.PublicHTTPURL(sl.URL)
		if err != nil {
			writeError(w, invalid("layer url is not a usable http(s) url: %v", err))
			return
		}
		// A suffix (e.g. Esri's "export") extends the endpoint path, staying on the
		// same host. Reject any "../" traversal defensively even though the host is
		// fixed by PublicHTTPURL and can't be walked off.
		if suffix := chi.URLParam(r, "*"); suffix != "" {
			if strings.Contains(suffix, "..") {
				writeError(w, invalid("proxy path may not contain '..'"))
				return
			}
			u = u.JoinPath(suffix)
		}
		// Merge the request's query over the endpoint's own (request wins), so a
		// base url that already carries params (e.g. a MapServer ?MAP=...) keeps
		// them while OL's GetMap/GetTile/export params are added.
		merged := u.Query()
		for k, vs := range r.URL.Query() {
			merged[k] = vs
		}
		u.RawQuery = merged.Encode()
		forward(w, r, s, sl, u)
	}
}

// loadEnabled loads the layer for a proxy request, treating a disabled layer as
// absent (404) -- the map should never be rendering a layer an admin turned off,
// and we don't want the proxy to be a usable fetch path for a disabled row.
func loadEnabled(r *http.Request, s *store) (*ServiceLayer, error) {
	sl, err := getRow(r.Context(), s, chi.URLParam(r, "id"))
	if err != nil {
		return nil, err
	}
	if !sl.IsEnabled {
		return nil, notFound("service layer %q is disabled", sl.Code)
	}
	return sl, nil
}

// forward performs the vetted outbound request and streams the response back with
// its content type and status preserved. The credential is decrypted here, used
// once, and never written to the response.
func forward(w http.ResponseWriter, r *http.Request, s *store, sl *ServiceLayer, target *url.URL) {
	authz, err := s.authFor(sl)
	if err != nil {
		writeError(w, err)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		writeError(w, upstream("could not build the upstream request: %v", err))
		return
	}
	// Relay the client's Accept so a service that content-negotiates returns the
	// image/format the map asked for; default to anything.
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	} else {
		req.Header.Set("Accept", "*/*")
	}
	if err := authz.Authorize(req); err != nil {
		writeError(w, invalid("layer %q auth is misconfigured: %v", sl.Code, err))
		return
	}

	resp, err := outbound.SafeClient(proxyTimeout).Do(req)
	if err != nil {
		writeError(w, upstream("could not reach the service for layer %q: %v", sl.Code, err))
		return
	}
	defer resp.Body.Close()

	// Relay the upstream content type (image/png, image/jpeg, application/xml for a
	// WMS ServiceException, ...) so OL parses the body correctly. Pass the status
	// through too: a WMS error is often a 200 with an XML body, while a real 4xx/5xx
	// should surface as such to the tile loader.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "" {
		w.Header().Set("Cache-Control", cc)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxProxyBytes))
}

// authFor turns a layer's stored auth config + sealed secret into an
// outbound.Auth, decrypting the secret only here. 'none' needs nothing; every
// other type needs a stored, decryptable secret, so a missing secret or a nil
// cipher (no encryption key on this server) is a clear error rather than a silent
// unauthenticated call to the upstream.
func (s *store) authFor(sl *ServiceLayer) (outbound.Auth, error) {
	if sl.AuthType == "" || sl.AuthType == "none" {
		return outbound.Auth{Type: "none"}, nil
	}
	if len(sl.CredentialEnc) == 0 {
		return outbound.Auth{}, invalid("layer %q has auth_type %s but no stored credential", sl.Code, sl.AuthType)
	}
	if s.cipher == nil {
		return outbound.Auth{}, invalid("cannot use layer %q: the server has no encryption key configured (set %s)", sl.Code, secretbox.EnvKey)
	}
	secret, err := s.cipher.Decrypt(sl.CredentialEnc)
	if err != nil {
		return outbound.Auth{}, upstream("could not decrypt the credential for layer %q (was the encryption key rotated?)", sl.Code)
	}
	return outbound.Auth{
		Type:       sl.AuthType,
		Header:     deref(sl.AuthHeader),
		QueryParam: deref(sl.AuthQueryParam),
		Username:   deref(sl.AuthUsername),
		Secret:     string(secret),
	}, nil
}
