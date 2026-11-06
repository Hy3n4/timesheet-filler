package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

const googleIssuer = "https://accounts.google.com"

// google discovers the provider lazily, so a Google outage at startup does
// not keep the app (and password sign-in) down.
type google struct {
	clientID, clientSecret, redirectURL string

	mu       sync.Mutex
	config   *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func newGoogle(clientID, clientSecret, redirectURL string) *google {
	return &google{clientID: clientID, clientSecret: clientSecret, redirectURL: redirectURL}
}

func (g *google) init(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.config == nil {
		provider, err := oidc.NewProvider(ctx, googleIssuer)
		if err != nil {
			return nil, nil, err
		}
		g.config = &oauth2.Config{
			ClientID:     g.clientID,
			ClientSecret: g.clientSecret,
			RedirectURL:  g.redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
		g.verifier = provider.Verifier(&oidc.Config{ClientID: g.clientID})
	}
	return g.config, g.verifier, nil
}

const googleStateCookie = "tf_google_state"

// GoogleStart redirects to Google. inviteToken, when set, accepts that invite
// with the Google account the user picks.
func (s *Service) GoogleStart(w http.ResponseWriter, r *http.Request, inviteToken string) error {
	if s.google == nil {
		return ErrGoogleFailed
	}
	ctx := r.Context()
	config, _, err := s.google.init(ctx)
	if err != nil {
		log.Printf("google discovery: %v", err)
		return ErrGoogleFailed
	}
	var inviteID *int64
	if inviteToken != "" {
		inv, err := s.InviteByToken(ctx, inviteToken)
		if err != nil {
			return err
		}
		inviteID = &inv.ID
	}
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	if _, err := s.pool.Exec(ctx, `INSERT INTO oauth_states (state_hash, nonce, pkce_verifier, invite_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, hashToken(state), nonce, verifier, inviteID, s.now().Add(oauthStateTTL)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: googleStateCookie, Value: state, Path: "/auth/google", MaxAge: int(oauthStateTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "select_account")), http.StatusFound)
	return nil
}

type googleClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// GoogleCallback finishes the flow started by GoogleStart and signs the user in.
func (s *Service) GoogleCallback(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	if s.google == nil {
		return ErrGoogleFailed
	}
	q := r.URL.Query()
	state := q.Get("state")
	cookie, err := r.Cookie(googleStateCookie)
	http.SetCookie(w, &http.Cookie{Name: googleStateCookie, Path: "/auth/google", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		return ErrGoogleFailed
	}
	var nonce, verifier string
	var inviteID *int64
	err = s.pool.QueryRow(ctx, `DELETE FROM oauth_states WHERE state_hash = $1 AND expires_at > now()
		RETURNING nonce, pkce_verifier, invite_id`, hashToken(state)).Scan(&nonce, &verifier, &inviteID)
	if err != nil {
		return ErrGoogleFailed
	}
	if q.Get("error") != "" {
		return ErrGoogleFailed
	}
	claims, err := s.exchangeGoogle(ctx, q.Get("code"), verifier, nonce)
	if err != nil {
		return err
	}

	var u *User
	if inviteID != nil {
		u, err = s.acceptInviteGoogle(ctx, *inviteID, claims)
	} else {
		u, err = s.googleUser(ctx, claims)
	}
	if err != nil {
		return err
	}
	if u.Disabled {
		return ErrDisabled
	}
	if _, err := s.pool.Exec(ctx, `UPDATE users SET display_name = $2, last_login_at = now() WHERE id = $1`,
		u.ID, claims.Name); err != nil {
		return err
	}
	s.audit(ctx, &u.ID, "login.google", "", ClientIP(r))
	return s.startSession(ctx, w, u.ID, StageFull)
}

func (s *Service) exchangeGoogle(ctx context.Context, code, verifier, nonce string) (*googleClaims, error) {
	config, idVerifier, err := s.google.init(ctx)
	if err != nil {
		return nil, ErrGoogleFailed
	}
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		log.Printf("google exchange: %v", err)
		return nil, ErrGoogleFailed
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, ErrGoogleFailed
	}
	idToken, err := idVerifier.Verify(ctx, raw)
	if err != nil {
		log.Printf("google id token: %v", err)
		return nil, ErrGoogleFailed
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return nil, ErrGoogleFailed
	}
	var claims googleClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, ErrGoogleFailed
	}
	if !claims.EmailVerified {
		return nil, ErrEmailNotVerified
	}
	claims.Email = strings.ToLower(claims.Email)
	return &claims, nil
}

// googleUser finds the account linked to the Google subject. Accounts are
// never linked by email alone: only an invite or ADMIN_EMAILS creates one.
func (s *Service) googleUser(ctx context.Context, c *googleClaims) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.google_sub = $1`, c.Subject))
	if !errors.Is(err, ErrNotFound) {
		return u, err
	}
	if !s.isBootstrapAdmin(c.Email) {
		return nil, ErrNoAccount
	}
	u, err = scanUser(s.pool.QueryRow(ctx, `INSERT INTO users AS u (email, display_name, role, google_sub)
		VALUES ($1, $2, 'admin', $3)
		ON CONFLICT DO NOTHING
		RETURNING `+userColumns, c.Email, c.Name, c.Subject))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNoAccount
	}
	return u, err
}

func (s *Service) acceptInviteGoogle(ctx context.Context, inviteID int64, c *googleClaims) (*User, error) {
	var u *User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		inv, err := lockInvite(ctx, tx, "id = $1", inviteID)
		if err != nil {
			return err
		}
		if !strings.EqualFold(inv.Email, c.Email) {
			return ErrEmailMismatch
		}
		u, err = insertUser(ctx, tx, inv, c.Name, nil, &c.Subject)
		return err
	})
	return u, err
}
