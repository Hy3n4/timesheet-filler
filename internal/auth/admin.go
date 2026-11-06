package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Service) Users(ctx context.Context) ([]*User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM users u ORDER BY u.role, lower(u.email)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*User, error) { return scanUser(row) })
}

// adminChange runs change on the target user inside a transaction that also
// refuses to remove the last active admin.
func (s *Service) adminChange(r *http.Request, id int64, action string, allowSelf bool,
	change func(tx pgx.Tx, target *User) error,
) error {
	ctx := r.Context()
	actor := UserFrom(ctx)
	if !allowSelf && actor.ID == id {
		return ErrSelf
	}
	var target *User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		var err error
		target, err = scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = $1`, id))
		if err != nil {
			return err
		}
		if err := change(tx, target); err != nil {
			return err
		}
		var admins int
		if err := tx.QueryRow(ctx,
			"SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL").Scan(&admins); err != nil {
			return err
		}
		if admins == 0 {
			return ErrLastAdmin
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, &actor.ID, action, target.Email, ClientIP(r))
	return nil
}

func (s *Service) UpdateUser(r *http.Request, id int64, role Role, eosName string) error {
	actor := UserFrom(r.Context())
	if actor.ID == id && role != actor.Role {
		return ErrSelf
	}
	return s.adminChange(r, id, "user.updated", true, func(tx pgx.Tx, _ *User) error {
		_, err := tx.Exec(r.Context(), "UPDATE users SET role = $2, eos_name = $3 WHERE id = $1",
			id, role, strings.TrimSpace(eosName))
		return err
	})
}

func (s *Service) SetDisabled(r *http.Request, id int64, disabled bool) error {
	action := "user.enabled"
	var at *time.Time
	if disabled {
		action = "user.disabled"
		now := s.now()
		at = &now
	}
	return s.adminChange(r, id, action, false, func(tx pgx.Tx, _ *User) error {
		if _, err := tx.Exec(r.Context(),
			"UPDATE users SET disabled_at = $2, failed_logins = 0, locked_until = NULL WHERE id = $1", id, at); err != nil {
			return err
		}
		return dropSessions(r.Context(), tx, id, disabled)
	})
}

// ResetMFA removes the authenticator and recovery codes; the user enrolls a
// new authenticator at the next password sign-in.
func (s *Service) ResetMFA(r *http.Request, id int64) error {
	return s.adminChange(r, id, "user.mfa_reset", false, func(tx pgx.Tx, _ *User) error {
		ctx := r.Context()
		if _, err := tx.Exec(ctx, `UPDATE users SET totp_secret_enc = NULL, totp_confirmed_at = NULL,
			totp_last_step = 0, failed_logins = 0, locked_until = NULL WHERE id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM recovery_codes WHERE user_id = $1", id); err != nil {
			return err
		}
		return dropSessions(ctx, tx, id, true)
	})
}

func (s *Service) DeleteUser(r *http.Request, id int64) error {
	return s.adminChange(r, id, "user.deleted", false, func(tx pgx.Tx, _ *User) error {
		_, err := tx.Exec(r.Context(), "DELETE FROM users WHERE id = $1", id)
		return err
	})
}

func dropSessions(ctx context.Context, tx pgx.Tx, userID int64, drop bool) error {
	if !drop {
		return nil
	}
	_, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
	return err
}

type AuditEntry struct {
	At     time.Time
	Actor  string
	Action string
	Target string
	IP     string
}

func (s *Service) AuditLog(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.at, COALESCE(u.email, ''), a.action, a.target, a.ip
		FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
		ORDER BY a.at DESC, a.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AuditEntry, error) {
		var e AuditEntry
		err := row.Scan(&e.At, &e.Actor, &e.Action, &e.Target, &e.IP)
		return e, err
	})
}
