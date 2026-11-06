package auth

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Invite struct {
	ID        int64
	Email     string
	Role      Role
	EosName   string
	CreatedAt time.Time
	ExpiresAt time.Time
	InvitedBy string
}

func normalizeEmail(email string) (string, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || addr.Name != "" {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(addr.Address), nil
}

// CreateInvite replaces any pending invite for the same email and returns the
// token for the link. The token is stored only as a hash.
func (s *Service) CreateInvite(r *http.Request, email string, role Role, eosName string) (string, error) {
	return s.createInvite(r.Context(), &UserFrom(r.Context()).ID, email, role, eosName, ClientIP(r))
}

// BootstrapAdminInvite creates an admin invite without an acting user, for
// the first admin when Google sign-in is not configured.
func (s *Service) BootstrapAdminInvite(ctx context.Context, email string) (string, error) {
	return s.createInvite(ctx, nil, email, RoleAdmin, "", "cli")
}

func (s *Service) createInvite(ctx context.Context, actorID *int64, email string, role Role, eosName, ip string) (string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return "", err
	}
	token := randomToken()
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = $1)", email).
			Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrUserExists
		}
		if _, err := tx.Exec(ctx, `UPDATE invites SET revoked_at = now()
			WHERE lower(email) = $1 AND accepted_at IS NULL AND revoked_at IS NULL`, email); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO invites (token_hash, email, role, eos_name, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			hashToken(token), email, role, strings.TrimSpace(eosName), actorID, s.now().Add(inviteTTL))
		return err
	})
	if err != nil {
		return "", err
	}
	s.audit(ctx, actorID, "invite.created", email+" ("+string(role)+")", ip)
	return token, nil
}

// RenewInvite issues a new token and expiry for a pending invite; the old
// link stops working.
func (s *Service) RenewInvite(r *http.Request, id int64) (string, *Invite, error) {
	ctx := r.Context()
	token := randomToken()
	var inv Invite
	err := s.pool.QueryRow(ctx, `UPDATE invites SET token_hash = $2, expires_at = $3
		WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL
		RETURNING id, email, role, eos_name`, id, hashToken(token), s.now().Add(inviteTTL)).
		Scan(&inv.ID, &inv.Email, &inv.Role, &inv.EosName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	s.audit(ctx, &UserFrom(ctx).ID, "invite.renewed", inv.Email, ClientIP(r))
	return token, &inv, nil
}

func (s *Service) RevokeInvite(r *http.Request, id int64) error {
	ctx := r.Context()
	var email string
	err := s.pool.QueryRow(ctx, `UPDATE invites SET revoked_at = now()
		WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL RETURNING email`, id).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	s.audit(ctx, &UserFrom(ctx).ID, "invite.revoked", email, ClientIP(r))
	return nil
}

func (s *Service) PendingInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `SELECT i.id, i.email, i.role, i.eos_name, i.created_at, i.expires_at,
		COALESCE(u.email, '')
		FROM invites i LEFT JOIN users u ON u.id = i.invited_by
		WHERE i.accepted_at IS NULL AND i.revoked_at IS NULL
		ORDER BY i.created_at DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invite, error) {
		var i Invite
		err := row.Scan(&i.ID, &i.Email, &i.Role, &i.EosName, &i.CreatedAt, &i.ExpiresAt, &i.InvitedBy)
		return i, err
	})
}

// InviteByToken returns the pending, unexpired invite for token.
func (s *Service) InviteByToken(ctx context.Context, token string) (*Invite, error) {
	var inv Invite
	err := s.pool.QueryRow(ctx, `SELECT id, email, role, eos_name, created_at, expires_at FROM invites
		WHERE token_hash = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()`,
		hashToken(token)).Scan(&inv.ID, &inv.Email, &inv.Role, &inv.EosName, &inv.CreatedAt, &inv.ExpiresAt)
	if err != nil {
		return nil, ErrInviteInvalid
	}
	return &inv, nil
}

func lockInvite(ctx context.Context, tx pgx.Tx, where string, arg any) (*Invite, error) {
	var inv Invite
	err := tx.QueryRow(ctx, `SELECT id, email, role, eos_name FROM invites
		WHERE `+where+` AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
		FOR UPDATE`, arg).Scan(&inv.ID, &inv.Email, &inv.Role, &inv.EosName)
	if err != nil {
		return nil, ErrInviteInvalid
	}
	return &inv, nil
}

func insertUser(ctx context.Context, tx pgx.Tx, inv *Invite, name string, passwordHash, googleSub *string) (*User, error) {
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users AS u (email, display_name, eos_name, role, password_hash, google_sub)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+userColumns,
		inv.Email, name, inv.EosName, inv.Role, passwordHash, googleSub))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrUserExists
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, "UPDATE invites SET accepted_at = now(), accepted_user_id = $2 WHERE id = $1", inv.ID, u.ID)
	return u, err
}

// AcceptInvitePassword creates a password account from an invite and starts
// the mandatory authenticator enrollment.
func (s *Service) AcceptInvitePassword(w http.ResponseWriter, r *http.Request, token, name, password string) error {
	ctx := r.Context()
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	var u *User
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		inv, err := lockInvite(ctx, tx, "token_hash = $1", hashToken(token))
		if err != nil {
			return err
		}
		u, err = insertUser(ctx, tx, inv, strings.TrimSpace(name), &hash, nil)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, &u.ID, "invite.accepted", "password", ClientIP(r))
	return s.startSession(ctx, w, u.ID, StageEnroll)
}
