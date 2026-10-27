package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

// oidcFlowCookie carries the per-attempt state/nonce/PKCE-verifier across
// the redirect to the IdP and back. It MUST be SameSite=Lax, not Strict:
// the callback arrives as a top-level cross-site navigation from the IdP,
// and a Strict cookie would not be sent, breaking every SSO login.
//
// Path MUST be "/", not "/auth/oidc": the backend's routes are reached under a
// path PREFIX the app's reverse proxy adds (the dev Vite proxy serves the API at
// /api/* and rewrites it to the backend's bare /auth/..., and a prod gateway may
// mount the API elsewhere). A cookie scoped to "/auth/oidc" is never sent on the
// browser-facing /api/auth/oidc/.../callback, so the callback sees no flow and
// fails "expired". "/" is prefix-agnostic. It stays httpOnly + short-lived (10
// min) and is cleared on the callback, so riding along on other calls is
// harmless.
const (
	oidcFlowCookie = "ve_oidc_flow"
	oidcFlowPath   = "/"
	oidcFlowTTL    = 10 * time.Minute
)

// loginErrorRedirect is where a failed SSO attempt sends the browser. The
// frontend login screen (Slice D) reads ?error to show a message; until
// then it is simply a stable, credential-free landing.
func loginErrorRedirect(reason string) string { return "/login?error=" + reason }

type oidcFlowState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
}

type providerInfo struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
}

// providersHandler is GET /auth/providers -- the login screen asks this
// which SSO buttons to render and whether the local form is currently
// usable. Public (there is no session yet at the login screen).
func (s *service) providersHandler(w http.ResponseWriter, r *http.Request) {
	providers := make([]providerInfo, 0, len(s.oidc))
	for _, p := range s.oidc {
		providers = append(providers, providerInfo{Key: p.key, DisplayName: p.displayName})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Key < providers[j].Key })

	// The switch governs ordinary local accounts; break-glass admins can
	// always sign in locally regardless. The screen still shows the form,
	// but this lets it hint the current policy.
	localEnabled, _ := s.localAuthEnabled(r.Context())
	// When local sign-in is off AND a break-glass reveal code is configured, the
	// login screen hides the password form behind that code (a soft second
	// factor). Without a code configured it just shows the form with a note, as
	// before, so a break-glass admin is never locked out of an emergency login.
	breakGlassCodeRequired := !localEnabled && s.cfg.BreakGlassReveal != ""
	writeJSON(w, http.StatusOK, map[string]any{
		"providers":                 providers,
		"local_auth_enabled":        localEnabled,
		"break_glass_code_required": breakGlassCodeRequired,
	})
}

// breakGlassRevealHandler is POST /auth/break-glass/reveal -- the login screen
// posts the typed reveal code here before showing the password form, so a wrong
// code is rejected up front (the login endpoint re-checks it, so this is a UX
// convenience, not the real gate). 200 = correct (reveal the form); 401 = wrong.
// A no-op 404-ish "not applicable" is returned as 400 when the feature is off.
func (s *service) breakGlassRevealHandler(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BreakGlassReveal == "" {
		writeError(w, invalid("break-glass reveal code is not configured"))
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Code), []byte(s.cfg.BreakGlassReveal)) != 1 {
		s.logEvent(r.Context(), r, eventLoginFailure, "", "break-glass reveal code rejected")
		writeError(w, unauthorized("invalid reveal code"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revealed": true})
}

// oidcLoginHandler is GET /auth/oidc/{provider}/login -- it starts the
// auth-code + PKCE flow: mint state (CSRF for the callback), nonce (replay
// defense, bound into the ID token), and a PKCE verifier; stash them in the
// flow cookie; redirect to the provider's authorization endpoint.
func (s *service) oidcLoginHandler(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "provider")
	provider, ok := s.oidc[key]
	if !ok {
		http.Redirect(w, r, loginErrorRedirect("unknown_provider"), http.StatusSeeOther)
		return
	}

	state, err1 := newRandomToken(32)
	nonce, err2 := newRandomToken(32)
	if err1 != nil || err2 != nil {
		writeError(w, errors.New("auth: could not start SSO"))
		return
	}
	verifier := oauth2.GenerateVerifier()

	s.setFlowCookie(w, oidcFlowState{Provider: key, State: state, Nonce: nonce, Verifier: verifier})

	authURL := provider.oauth2.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
		oauth2.AccessTypeOnline,
	)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// oidcCallbackHandler is GET /auth/oidc/{provider}/callback -- the IdP
// redirects here with a code. It validates the flow (state), exchanges the
// code (PKCE), verifies the ID token (signature/issuer/audience via the
// verifier, then nonce here), applies provider trust rules, matches or
// rejects the account, and on success issues the same session as any other
// login. Every failure is audited and redirected to the login screen; no
// internal detail reaches the browser.
func (s *service) oidcCallbackHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := chi.URLParam(r, "provider")
	provider, ok := s.oidc[key]
	if !ok {
		http.Redirect(w, r, loginErrorRedirect("unknown_provider"), http.StatusSeeOther)
		return
	}

	flow, err := s.readFlowCookie(r)
	s.clearFlowCookie(w)
	if err != nil || flow.Provider != key {
		http.Redirect(w, r, loginErrorRedirect("expired"), http.StatusSeeOther)
		return
	}

	// Provider-side error (user cancelled, consent denied, ...).
	if e := r.URL.Query().Get("error"); e != "" {
		s.logEvent(ctx, r, eventLoginFailure, "", "SSO provider error via "+key+": "+e)
		http.Redirect(w, r, loginErrorRedirect("sso"), http.StatusSeeOther)
		return
	}

	// State: double-submit against the flow cookie (CSRF for the callback).
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(flow.State)) != 1 {
		s.logEvent(ctx, r, eventLoginFailure, "", "SSO state mismatch via "+key)
		http.Redirect(w, r, loginErrorRedirect("state"), http.StatusSeeOther)
		return
	}

	token, err := provider.oauth2.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		s.logEvent(ctx, r, eventLoginFailure, "", "SSO code exchange failed via "+key)
		http.Redirect(w, r, loginErrorRedirect("sso"), http.StatusSeeOther)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		s.logEvent(ctx, r, eventLoginFailure, "", "SSO response had no id_token via "+key)
		http.Redirect(w, r, loginErrorRedirect("sso"), http.StatusSeeOther)
		return
	}
	idToken, err := provider.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		s.logEvent(ctx, r, eventLoginFailure, "", "SSO id_token verification failed via "+key)
		http.Redirect(w, r, loginErrorRedirect("sso"), http.StatusSeeOther)
		return
	}
	// Nonce binds this token to the flow we started (replay defense).
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 {
		s.logEvent(ctx, r, eventLoginFailure, "", "SSO nonce mismatch via "+key)
		http.Redirect(w, r, loginErrorRedirect("sso"), http.StatusSeeOther)
		return
	}

	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		http.Redirect(w, r, loginErrorRedirect("sso"), http.StatusSeeOther)
		return
	}
	claims.Subject = idToken.Subject // trust the verified subject, not the raw claim
	if err := provider.validateClaims(claims); err != nil {
		s.logEvent(ctx, r, eventLoginDenied, "", "SSO claim validation failed via "+key+": "+err.Error())
		http.Redirect(w, r, loginErrorRedirect("denied"), http.StatusSeeOther)
		return
	}

	user, err := s.authenticateOIDC(ctx, r, key, claims)
	if err != nil {
		if isForbiddenError(err) || isUnauthorizedError(err) {
			http.Redirect(w, r, loginErrorRedirect("no_account"), http.StatusSeeOther)
			return
		}
		writeError(w, err)
		return
	}

	rawToken, _, err := s.createSession(ctx, user.ID, r)
	if err != nil {
		writeError(w, err)
		return
	}
	loaded, err := s.loadSession(ctx, rawToken)
	if err != nil || loaded == nil {
		writeError(w, errors.New("auth: could not establish session"))
		return
	}
	s.setSessionCookies(w, rawToken, loaded.CSRFToken)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *service) setFlowCookie(w http.ResponseWriter, flow oidcFlowState) {
	raw, _ := json.Marshal(flow)
	http.SetCookie(w, &http.Cookie{
		Name:     oidcFlowCookie,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     oidcFlowPath,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oidcFlowTTL / time.Second),
	})
}

func (s *service) readFlowCookie(r *http.Request) (oidcFlowState, error) {
	var flow oidcFlowState
	c, err := r.Cookie(oidcFlowCookie)
	if err != nil {
		return flow, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return flow, err
	}
	if err := json.Unmarshal(raw, &flow); err != nil {
		return flow, err
	}
	return flow, nil
}

func (s *service) clearFlowCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcFlowCookie,
		Value:    "",
		Path:     oidcFlowPath,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
