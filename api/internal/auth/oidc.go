package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// This file is Slice B: OIDC federation (Azure AD + Google). It builds one
// or more SSO providers from the environment at startup, and implements
// the SSO half of the login decision. Both providers funnel into the exact
// same session this package already issues for local login -- authorization
// (Slice C) never learns, or cares, how a user authenticated.
//
// The provisioning rule is the locked decision from docs/phase-10-auth.md:
// accounts are pre-provisioned by an admin, and SSO NEVER auto-creates a
// user. A verified identity is either matched to an existing, active
// account (by a previously-linked subject, or by email on first login) or
// rejected outright.

// oidcProvider is one configured SSO provider: its OAuth2 client and its
// ID-token verifier (JWKS-backed, from go-oidc's discovery). allowedDomain
// is Google's hosted-domain restriction ("" = unrestricted); Azure is
// scoped to a single tenant by its issuer URL, so it needs no analogue.
type oidcProvider struct {
	key           string
	displayName   string
	oauth2        oauth2.Config
	verifier      *oidc.IDTokenVerifier
	allowedDomain string
}

// oidcClaims is the subset of ID-token claims we read. Subject is the
// provider's stable per-user id (verified: taken from the token, not this
// struct). email_verified gates Google; Azure carries the address in
// preferred_username (the UPN) when there is no email claim.
type oidcClaims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	HostedDomain      string `json:"hd"`
}

// resolvedEmail is the lower-cased address we match an account on: the
// email claim if present, otherwise the UPN Azure puts in
// preferred_username.
func (c oidcClaims) resolvedEmail() string {
	if e := strings.TrimSpace(c.Email); e != "" {
		return strings.ToLower(e)
	}
	if u := strings.TrimSpace(c.PreferredUsername); strings.Contains(u, "@") {
		return strings.ToLower(u)
	}
	return ""
}

// buildOIDCRegistry constructs every provider that is fully configured in
// the environment, doing the one-time discovery (JWKS + endpoints) per
// provider. A provider that is absent or fails discovery is simply not
// registered -- so it never appears on the login screen, and local-only or
// single-provider deployments are all valid. Called once by Routes at
// startup.
func buildOIDCRegistry(ctx context.Context, _ Config) map[string]*oidcProvider {
	reg := map[string]*oidcProvider{}

	if tenant := os.Getenv("OIDC_AZURE_TENANT_ID"); tenant != "" && os.Getenv("OIDC_AZURE_CLIENT_ID") != "" {
		if p, err := newOIDCProvider(ctx, oidcProviderParams{
			key:          "azuread",
			displayName:  "Microsoft",
			issuer:       fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenant),
			clientID:     os.Getenv("OIDC_AZURE_CLIENT_ID"),
			clientSecret: os.Getenv("OIDC_AZURE_CLIENT_SECRET"),
			redirectURL:  os.Getenv("OIDC_AZURE_REDIRECT_URL"),
			scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}); err != nil {
			slog.Warn("SSO provider not enabled", "component", "auth", "provider", "azure", "err", err)
		} else {
			reg[p.key] = p
		}
	}

	if os.Getenv("OIDC_GOOGLE_CLIENT_ID") != "" {
		if p, err := newOIDCProvider(ctx, oidcProviderParams{
			key:           "google",
			displayName:   "Google",
			issuer:        "https://accounts.google.com",
			clientID:      os.Getenv("OIDC_GOOGLE_CLIENT_ID"),
			clientSecret:  os.Getenv("OIDC_GOOGLE_CLIENT_SECRET"),
			redirectURL:   os.Getenv("OIDC_GOOGLE_REDIRECT_URL"),
			scopes:        []string{oidc.ScopeOpenID, "email", "profile"},
			allowedDomain: os.Getenv("OIDC_GOOGLE_ALLOWED_DOMAIN"),
		}); err != nil {
			slog.Warn("SSO provider not enabled", "component", "auth", "provider", "google", "err", err)
		} else {
			reg[p.key] = p
		}
	}

	if len(reg) == 0 {
		slog.Info("no SSO providers configured; local login only", "component", "auth")
	}
	return reg
}

type oidcProviderParams struct {
	key, displayName, issuer, clientID, clientSecret, redirectURL string
	scopes        []string
	allowedDomain string
}

func newOIDCProvider(ctx context.Context, p oidcProviderParams) (*oidcProvider, error) {
	if p.clientSecret == "" || p.redirectURL == "" {
		return nil, errors.New("client secret and redirect URL are required")
	}
	provider, err := oidc.NewProvider(ctx, p.issuer)
	if err != nil {
		return nil, fmt.Errorf("discovery for %s: %w", p.issuer, err)
	}
	return &oidcProvider{
		key:         p.key,
		displayName: p.displayName,
		oauth2: oauth2.Config{
			ClientID:     p.clientID,
			ClientSecret: p.clientSecret,
			RedirectURL:  p.redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       p.scopes,
		},
		verifier:      provider.Verifier(&oidc.Config{ClientID: p.clientID}),
		allowedDomain: p.allowedDomain,
	}, nil
}

// validateClaims applies the provider-specific trust rules that go beyond
// signature/issuer/audience (which the verifier already enforced). Google:
// the email must be verified, and if a hosted domain is configured the
// account must belong to it. Azure: the tenant-scoped issuer already
// bounds who can produce a valid token, so there is nothing extra here.
func (p *oidcProvider) validateClaims(c oidcClaims) error {
	if p.key == "google" {
		if !c.EmailVerified {
			return errors.New("google account email is not verified")
		}
		if p.allowedDomain != "" {
			email := c.resolvedEmail()
			domain := ""
			if at := strings.LastIndex(email, "@"); at >= 0 {
				domain = email[at+1:]
			}
			if !strings.EqualFold(c.HostedDomain, p.allowedDomain) && !strings.EqualFold(domain, p.allowedDomain) {
				return fmt.Errorf("account is not in the allowed domain %q", p.allowedDomain)
			}
		}
	}
	return nil
}

// authenticateOIDC is the SSO match-or-reject decision. It never creates a
// user. Order:
//
//  1. If this (provider, subject) is already linked, log that user in
//     (unless the account has since been disabled).
//  2. Otherwise match a pre-provisioned, active account by email and link
//     the SSO identity to it on this first login.
//  3. Otherwise reject -- there is no VoltEdge account for this identity.
func (s *service) authenticateOIDC(ctx context.Context, r *http.Request, providerKey string, c oidcClaims) (*User, error) {
	subject := c.Subject
	if subject == "" {
		return nil, unauthorized("the identity provider returned no subject")
	}
	email := c.resolvedEmail()

	// 1. Known identity -> straight in.
	var identity Identity
	err := s.db.NewSelect().Model(&identity).
		Where("provider = ?", providerKey).
		Where("provider_subject = ?", subject).
		Scan(ctx)
	if err == nil {
		var user User
		e := s.db.NewSelect().Model(&user).
			Where("id = ?", identity.UserID).
			Where("status = ?", "active").
			Scan(ctx)
		if errors.Is(e, sql.ErrNoRows) {
			s.logEvent(ctx, r, eventLoginDenied, identity.UserID, "SSO login for disabled account via "+providerKey)
			return nil, forbidden("this account is disabled")
		}
		if e != nil {
			return nil, e
		}
		s.logEvent(ctx, r, eventLoginSuccess, user.ID, "SSO via "+providerKey)
		return &user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// 2. No identity yet -> match a pre-provisioned account by email, then
	// link on this first login. Accounts are never auto-created.
	if email == "" {
		return nil, forbidden("the identity provider returned no email to match an account")
	}
	var user User
	err = s.db.NewSelect().Model(&user).
		Where("email = ?", email).
		Where("status = ?", "active").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		s.logEvent(ctx, r, eventLoginDenied, "", "SSO rejected: no pre-provisioned account for "+email+" via "+providerKey)
		return nil, forbidden("no VoltEdge account exists for this identity; ask an administrator to provision one")
	}
	if err != nil {
		return nil, err
	}

	newIdentity := &Identity{UserID: user.ID, Provider: providerKey, ProviderSubject: &subject}
	if _, err := s.db.NewInsert().Model(newIdentity).Exec(ctx); err != nil {
		return nil, err
	}
	s.logEvent(ctx, r, eventSSOLinked, user.ID, "linked "+providerKey+" identity")
	s.logEvent(ctx, r, eventLoginSuccess, user.ID, "SSO via "+providerKey+" (first login)")
	return &user, nil
}
