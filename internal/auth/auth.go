// Package auth implements accounts, sessions, invitations, Google sign-in and
// second factors for password accounts. State lives in PostgreSQL; secrets
// that must be recoverable (TOTP seeds) are sealed with the secretbox keyring.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timesheet-filler/internal/secretbox"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

func ParseRole(s string) (Role, error) {
	switch Role(s) {
	case RoleAdmin, RoleMember:
		return Role(s), nil
	}
	return "", ErrInvalidRole
}

// Stage is how far a session got through sign-in. Only StageFull sessions
// reach the application.
type Stage string

const (
	StageMFA    Stage = "mfa"
	StageEnroll Stage = "enroll"
	StageFull   Stage = "full"
)

type User struct {
	ID          int64
	Email       string
	DisplayName string
	EosName     string
	Role        Role
	HasGoogle   bool
	HasPassword bool
	HasTOTP     bool
	Disabled    bool
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Errors returned to handlers. Their messages are translation keys.
var (
	ErrInvalidCredentials = errors.New("auth_err_invalid_credentials")
	ErrLocked             = errors.New("auth_err_locked")
	ErrDisabled           = errors.New("auth_err_disabled")
	ErrInvalidCode        = errors.New("auth_err_invalid_code")
	ErrInviteInvalid      = errors.New("auth_err_invite_invalid")
	ErrNoAccount          = errors.New("auth_err_no_account")
	ErrEmailMismatch      = errors.New("auth_err_email_mismatch")
	ErrEmailNotVerified   = errors.New("auth_err_email_not_verified")
	ErrWeakPassword       = errors.New("auth_err_weak_password")
	ErrUserExists         = errors.New("auth_err_user_exists")
	ErrInvalidEmail       = errors.New("auth_err_invalid_email")
	ErrInvalidRole        = errors.New("auth_err_invalid_role")
	ErrTooSoon            = errors.New("auth_err_too_soon")
	ErrSelf               = errors.New("auth_err_self")
	ErrLastAdmin          = errors.New("auth_err_last_admin")
	ErrGoogleFailed       = errors.New("auth_err_google_failed")
	ErrNotFound           = errors.New("auth_err_not_found")
)

const (
	maxFailedLogins   = 5
	lockDuration      = 15 * time.Minute
	pendingSessionTTL = 15 * time.Minute
	inviteTTL         = 7 * 24 * time.Hour
	oauthStateTTL     = 10 * time.Minute
	emailCodeTTL      = 10 * time.Minute
	emailCodeInterval = time.Minute
	maxCodeAttempts   = 5
	minPasswordLength = 12
	maxPasswordLength = 256
)

type Config struct {
	BaseURL            string
	GoogleClientID     string
	GoogleClientSecret string
	AdminEmails        []string
	CookieSecure       bool
	SessionTTL         time.Duration
	SessionIdle        time.Duration
}

// Mailer is the subset of the email service auth needs.
type Mailer interface {
	IsConfigured() bool
	SendEmail(subject, body string, to, cc []string) error
}

type Service struct {
	pool   *pgxpool.Pool
	keys   *secretbox.Keyring
	cfg    Config
	mailer Mailer
	google *google
	now    func() time.Time
}

func NewService(pool *pgxpool.Pool, keys *secretbox.Keyring, cfg Config, mailer Mailer) *Service {
	s := &Service{pool: pool, keys: keys, cfg: cfg, mailer: mailer, now: time.Now}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		s.google = newGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, strings.TrimRight(cfg.BaseURL, "/")+"/auth/google/callback")
	}
	return s
}

func (s *Service) GoogleEnabled() bool { return s.google != nil }

func (s *Service) Mailer() Mailer { return s.mailer }

func (s *Service) EmailEnabled() bool { return s.mailer != nil && s.mailer.IsConfigured() }

func (s *Service) InviteLink(token string) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/invite/" + token
}

func (s *Service) isBootstrapAdmin(email string) bool {
	for _, a := range s.cfg.AdminEmails {
		if strings.EqualFold(strings.TrimSpace(a), email) {
			return true
		}
	}
	return false
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: random: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s *Service) audit(ctx context.Context, actor *int64, action, target, ip string) {
	_, _ = s.pool.Exec(ctx,
		"INSERT INTO audit_log (actor_id, action, target, ip) VALUES ($1, $2, $3, $4)",
		actor, action, target, ip)
}

// ClientIP is for the audit log only; X-Forwarded-For can be forged by the
// client, so never base an access decision on it.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
