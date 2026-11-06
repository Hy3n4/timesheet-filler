package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"timesheet-filler/internal/contextkeys"
	"timesheet-filler/internal/models"
)

type Session struct {
	User      *User
	Stage     Stage
	tokenHash []byte
}

type ctxKey struct{}

// SessionFrom returns the session loaded by Middleware, or nil.
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKey{}).(*Session)
	return s
}

// UserFrom returns the fully signed-in user, or nil.
func UserFrom(ctx context.Context) *User {
	if s := SessionFrom(ctx); s != nil && s.Stage == StageFull {
		return s.User
	}
	return nil
}

const userColumns = `u.id, u.email, u.display_name, u.eos_name, u.role,
	u.google_sub IS NOT NULL, u.password_hash IS NOT NULL, u.totp_confirmed_at IS NOT NULL,
	u.disabled_at IS NOT NULL, u.created_at, u.last_login_at`

func scanUser(row pgx.Row, extra ...any) (*User, error) {
	var u User
	dest := append([]any{&u.ID, &u.Email, &u.DisplayName, &u.EosName, &u.Role,
		&u.HasGoogle, &u.HasPassword, &u.HasTOTP, &u.Disabled, &u.CreatedAt, &u.LastLoginAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *Service) cookieName() string {
	if s.cfg.CookieSecure {
		return "__Host-tf_session"
	}
	return "tf_session"
}

// startSession creates a session and sets its cookie.
func (s *Service) startSession(ctx context.Context, w http.ResponseWriter, userID int64, stage Stage) error {
	ttl := pendingSessionTTL
	if stage == StageFull {
		ttl = s.cfg.SessionTTL
	}
	token := randomToken()
	expires := s.now().Add(ttl)
	if _, err := s.pool.Exec(ctx,
		"INSERT INTO sessions (token_hash, user_id, stage, expires_at) VALUES ($1, $2, $3, $4)",
		hashToken(token), userID, stage, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// upgrade replaces a pending session with a new one at stage, so the token
// that was in the browser before the second factor never becomes privileged.
func (s *Service) upgrade(ctx context.Context, w http.ResponseWriter, sess *Session, stage Stage) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", sess.tokenHash); err != nil {
		return err
	}
	if stage == StageFull {
		if _, err := s.pool.Exec(ctx,
			"UPDATE users SET last_login_at = now(), failed_logins = 0, locked_until = NULL WHERE id = $1",
			sess.User.ID); err != nil {
			return err
		}
	}
	return s.startSession(ctx, w, sess.User.ID, stage)
}

func (s *Service) loadSession(ctx context.Context, token string) (*Session, error) {
	hash := hashToken(token)
	var stage Stage
	var lastSeen time.Time
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+`, s.stage, s.last_seen_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, hash), &stage, &lastSeen)
	if err != nil {
		return nil, err
	}
	if u.Disabled || (stage == StageFull && s.now().Sub(lastSeen) > s.cfg.SessionIdle) {
		_, _ = s.pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", hash)
		return nil, ErrNotFound
	}
	if s.now().Sub(lastSeen) > 5*time.Minute {
		_, _ = s.pool.Exec(ctx, "UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1", hash)
	}
	return &Session{User: u, Stage: stage, tokenHash: hash}, nil
}

// Logout deletes the current session and its cookie.
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	if sess := SessionFrom(r.Context()); sess != nil {
		_, _ = s.pool.Exec(r.Context(), "DELETE FROM sessions WHERE token_hash = $1", sess.tokenHash)
		if sess.Stage == StageFull {
			s.audit(r.Context(), &sess.User.ID, "logout", "", ClientIP(r))
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

// PurgeExpired removes stale sessions, OAuth states and email codes.
func (s *Service) PurgeExpired(ctx context.Context) {
	for _, q := range []string{
		"DELETE FROM sessions WHERE expires_at < now()",
		"DELETE FROM oauth_states WHERE expires_at < now()",
		"DELETE FROM email_codes WHERE expires_at < now()",
	} {
		_, _ = s.pool.Exec(ctx, q)
	}
}

// Middleware loads the session named by the cookie, if any, into the request
// context. It never rejects a request; RequireUser and RequireAdmin do.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cookieName())
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := s.loadSession(r.Context(), c.Value)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, sess)
		if sess.Stage == StageFull {
			ctx = context.WithValue(ctx, contextkeys.ViewerKey, &models.Viewer{
				Email: sess.User.Email, Name: sess.User.DisplayName, IsAdmin: sess.User.IsAdmin(),
			})
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireUser sends anyone without a full session to the step of sign-in
// they still have to complete.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := SessionFrom(r.Context())
		switch {
		case sess == nil:
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		case sess.Stage == StageMFA:
			http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
		case sess.Stage == StageEnroll:
			http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func RequireAdmin(next http.Handler) http.Handler {
	return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := UserFrom(r.Context()); u == nil || !u.IsAdmin() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
