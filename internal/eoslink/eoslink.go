// Package eoslink keeps each user's link to their EOS account: the EOS
// refresh token (sealed with the secretbox keyring, bound to the user) and a
// cached access token. Tokens are decrypted only to call EOS for that user.
package eoslink

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"timesheet-filler/internal/eos"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/secretbox"
)

var (
	ErrNotLinked      = errors.New("eoslink: not linked")
	ErrReauthRequired = errors.New("eoslink: EOS login expired")
	ErrNoPendingLogin = errors.New("eoslink: no pending EOS login")
	ErrTooManyTries   = errors.New("eoslink: too many failed EOS logins")
)

const (
	pendingLoginTTL = 5 * time.Minute
	// The link form must not become a way to guess other people's EOS passwords.
	maxFailedLinks   = 5
	failedLinkWindow = 15 * time.Minute
	// Refresh a little before EOS would reject the access token.
	accessMargin = 2 * time.Minute
)

type Status string

const (
	StatusActive Status = "active"
	StatusReauth Status = "reauth"
)

type Link struct {
	MemberID    int64
	Status      Status
	LinkedAt    time.Time
	RefreshedAt *time.Time
}

type Service struct {
	pool   *pgxpool.Pool
	keys   *secretbox.Keyring
	client *eos.Client
	now    func() time.Time
}

func NewService(pool *pgxpool.Pool, keys *secretbox.Keyring, client *eos.Client) *Service {
	return &Service{pool: pool, keys: keys, client: client, now: time.Now}
}

func aad(kind string, userID int64) []byte { return fmt.Appendf(nil, "eos-%s:%d", kind, userID) }

func (s *Service) audit(ctx context.Context, userID int64, action, target, ip string) {
	_, _ = s.pool.Exec(ctx, "INSERT INTO audit_log (actor_id, action, target, ip) VALUES ($1, $2, $3, $4)",
		userID, action, target, ip)
}

// Get returns the user's link (nil when there is none) and whether an EOS
// login is waiting for its two-factor code.
func (s *Service) Get(ctx context.Context, userID int64) (*Link, bool, error) {
	var pending bool
	if err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM eos_pending_logins WHERE user_id = $1 AND expires_at > now())",
		userID).Scan(&pending); err != nil {
		return nil, false, err
	}
	var l Link
	err := s.pool.QueryRow(ctx, `SELECT member_id, status, linked_at, refreshed_at FROM eos_links WHERE user_id = $1`,
		userID).Scan(&l.MemberID, &l.Status, &l.LinkedAt, &l.RefreshedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pending, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &l, pending, nil
}

// Link logs in to EOS with the user's EOS credentials, which are used for this
// one request and never stored. It reports whether EOS asked for its
// two-factor code; CompleteOTP then finishes the link.
func (s *Service) Link(ctx context.Context, userID int64, email, password, ip string) (needOTP bool, err error) {
	if err := s.checkFailures(ctx, userID); err != nil {
		return false, err
	}
	res, err := s.client.Login(ctx, email, password)
	if errors.Is(err, eos.ErrInvalidCredentials) {
		s.audit(ctx, userID, "eos.link_failed", "", ip)
	}
	if err != nil {
		return false, err
	}
	if res.Tokens == nil {
		sealed, err := s.keys.Seal([]byte(res.AuthenticationCode), aad("login", userID))
		if err != nil {
			return false, err
		}
		_, err = s.pool.Exec(ctx, `INSERT INTO eos_pending_logins (user_id, code_enc, expires_at) VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET code_enc = $2, expires_at = $3`,
			userID, sealed, s.now().Add(pendingLoginTTL))
		return err == nil, err
	}
	return false, s.store(ctx, userID, res.Tokens, ip)
}

func (s *Service) checkFailures(ctx context.Context, userID int64) error {
	var failures int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log
		WHERE actor_id = $1 AND action = 'eos.link_failed' AND at > $2`,
		userID, s.now().Add(-failedLinkWindow)).Scan(&failures); err != nil {
		return err
	}
	if failures >= maxFailedLinks {
		return ErrTooManyTries
	}
	return nil
}

func (s *Service) CompleteOTP(ctx context.Context, userID int64, otp, ip string) error {
	if err := s.checkFailures(ctx, userID); err != nil {
		return err
	}
	var sealed string
	err := s.pool.QueryRow(ctx, `SELECT code_enc FROM eos_pending_logins WHERE user_id = $1 AND expires_at > now()`,
		userID).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoPendingLogin
	}
	if err != nil {
		return err
	}
	code, _, err := s.keys.Open(sealed, aad("login", userID))
	if err != nil {
		return err
	}
	tokens, err := s.client.CompleteOTP(ctx, string(code), otp)
	if errors.Is(err, eos.ErrInvalidCredentials) {
		s.audit(ctx, userID, "eos.link_failed", "otp", ip)
	}
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM eos_pending_logins WHERE user_id = $1", userID); err != nil {
		return err
	}
	return s.store(ctx, userID, tokens, ip)
}

// store saves a fresh login, ending the EOS session of a previous link first.
func (s *Service) store(ctx context.Context, userID int64, t *eos.Tokens, ip string) error {
	if _, err := s.unlink(ctx, userID); err != nil && !errors.Is(err, ErrNotLinked) {
		return err
	}
	refresh, err := s.keys.Seal([]byte(t.Refresh), aad("refresh", userID))
	if err != nil {
		return err
	}
	access, err := s.keys.Seal([]byte(t.Access), aad("access", userID))
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO eos_links
		(user_id, member_id, refresh_enc, refresh_session_id, access_enc, access_expires_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'active')`,
		userID, t.MemberID, refresh, nullIfZero(t.RefreshSessionID), access, t.AccessExpires); err != nil {
		return err
	}
	s.audit(ctx, userID, "eos.linked", fmt.Sprintf("member %d", t.MemberID), ip)
	return nil
}

func nullIfZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// Unlink deletes the link and asks EOS to end its session. ended is false when
// EOS could not be told; the session then stays listed in the user's EOS
// account until it expires or they remove it there.
func (s *Service) Unlink(ctx context.Context, userID int64, ip string) (ended bool, err error) {
	ended, err = s.unlink(ctx, userID)
	if err == nil {
		s.audit(ctx, userID, "eos.unlinked", "", ip)
	}
	return ended, err
}

func (s *Service) unlink(ctx context.Context, userID int64) (bool, error) {
	var refreshEnc string
	var accessEnc *string
	var sessionID *int64
	err := s.pool.QueryRow(ctx, `DELETE FROM eos_links WHERE user_id = $1
		RETURNING refresh_enc, access_enc, refresh_session_id`, userID).Scan(&refreshEnc, &accessEnc, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotLinked
	}
	if err != nil {
		return false, err
	}
	if sessionID == nil {
		return false, nil
	}
	refresh, _, err := s.keys.Open(refreshEnc, aad("refresh", userID))
	if err != nil {
		return false, nil
	}
	var access []byte
	if accessEnc != nil {
		access, _, _ = s.keys.Open(*accessEnc, aad("access", userID))
	}
	if err := s.client.DeleteSession(ctx, *sessionID, string(access), string(refresh)); err != nil {
		log.Printf("eoslink: ending EOS session of user %d: %v", userID, err)
		return false, nil
	}
	return true, nil
}

// AccessToken returns a valid EOS access token for the user, refreshing it
// when needed. The row lock makes concurrent requests of one user refresh once.
func (s *Service) AccessToken(ctx context.Context, userID int64) (string, int64, error) {
	var token string
	var memberID int64
	var reauth bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status Status
		var refreshEnc string
		var accessEnc *string
		var expires *time.Time
		err := tx.QueryRow(ctx, `SELECT member_id, status, refresh_enc, access_enc, access_expires_at
			FROM eos_links WHERE user_id = $1 FOR UPDATE`, userID).
			Scan(&memberID, &status, &refreshEnc, &accessEnc, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotLinked
		}
		if err != nil {
			return err
		}
		if status != StatusActive {
			return ErrReauthRequired
		}
		if accessEnc != nil && expires != nil && expires.After(s.now().Add(accessMargin)) {
			plain, _, err := s.keys.Open(*accessEnc, aad("access", userID))
			if err == nil {
				token = string(plain)
				return nil
			}
		}
		refresh, _, err := s.keys.Open(refreshEnc, aad("refresh", userID))
		if err != nil {
			return err
		}
		fresh, err := s.client.Refresh(ctx, string(refresh), memberID)
		if errors.Is(err, eos.ErrUnauthorized) {
			if _, err := tx.Exec(ctx, `UPDATE eos_links SET status = 'reauth', access_enc = NULL,
				access_expires_at = NULL WHERE user_id = $1`, userID); err != nil {
				return err
			}
			reauth = true
			return nil
		}
		if err != nil {
			return err
		}
		newRefresh, err := s.keys.Seal([]byte(fresh.Refresh), aad("refresh", userID))
		if err != nil {
			return err
		}
		newAccess, err := s.keys.Seal([]byte(fresh.Access), aad("access", userID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE eos_links SET refresh_enc = $2, access_enc = $3, access_expires_at = $4,
			refreshed_at = now() WHERE user_id = $1`, userID, newRefresh, newAccess, fresh.AccessExpires); err != nil {
			return err
		}
		token = fresh.Access
		return nil
	})
	if err == nil && reauth {
		s.audit(ctx, userID, "eos.reauth_required", "", "")
		return "", 0, ErrReauthRequired
	}
	return token, memberID, err
}

// MonthRows loads the user's attendance for a month from EOS as edit-page rows.
func (s *Service) MonthRows(ctx context.Context, userID int64, year int, month time.Month) ([]models.TableRow, error) {
	for attempt := 0; ; attempt++ {
		token, memberID, err := s.AccessToken(ctx, userID)
		if err != nil {
			return nil, err
		}
		m, err := s.client.FetchMonth(ctx, token, memberID, year, month)
		if errors.Is(err, eos.ErrUnauthorized) && attempt == 0 {
			// The cached access token was revoked (e.g. the session was ended in
			// EOS); drop it so the retry refreshes or marks the link for re-login.
			if _, err := s.pool.Exec(ctx,
				"UPDATE eos_links SET access_enc = NULL, access_expires_at = NULL WHERE user_id = $1", userID); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		rows, err := eos.BuildRows(*m)
		if err != nil {
			return nil, err
		}
		return eos.TableRows(rows), nil
	}
}
