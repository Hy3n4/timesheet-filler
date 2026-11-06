package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
)

type credentials struct {
	passwordHash *string
	totpSecret   *string
	totpLastStep int64
	lockedUntil  *time.Time
}

func (s *Service) userWithCredentials(ctx context.Context, where string, arg any) (*User, *credentials, error) {
	var c credentials
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+`,
		u.password_hash, u.totp_secret_enc, u.totp_last_step, u.locked_until
		FROM users u WHERE `+where, arg), &c.passwordHash, &c.totpSecret, &c.totpLastStep, &c.lockedUntil)
	if err != nil {
		return nil, nil, err
	}
	return u, &c, nil
}

func (s *Service) recordFailure(ctx context.Context, userID int64, ip string) {
	_, _ = s.pool.Exec(ctx, `UPDATE users SET
		failed_logins = failed_logins + 1,
		locked_until = CASE WHEN failed_logins + 1 >= $2 THEN now() + $3::interval ELSE locked_until END
		WHERE id = $1`, userID, maxFailedLogins, fmt.Sprintf("%d seconds", int(lockDuration.Seconds())))
	s.audit(ctx, &userID, "login.failed", "", ip)
}

// PasswordLogin checks email and password and starts a pending session: the
// second factor, or its enrollment when the user has none yet.
func (s *Service) PasswordLogin(w http.ResponseWriter, r *http.Request, email, password string) (Stage, error) {
	ctx := r.Context()
	u, c, err := s.userWithCredentials(ctx, "lower(u.email) = lower($1)", strings.TrimSpace(email))
	if errors.Is(err, ErrNotFound) || (err == nil && c.passwordHash == nil) {
		VerifyPassword(dummyHash, password)
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", err
	}
	if c.lockedUntil != nil && c.lockedUntil.After(s.now()) {
		return "", ErrLocked
	}
	if !VerifyPassword(*c.passwordHash, password) {
		s.recordFailure(ctx, u.ID, ClientIP(r))
		return "", ErrInvalidCredentials
	}
	if u.Disabled {
		return "", ErrDisabled
	}
	stage := StageMFA
	if !u.HasTOTP {
		stage = StageEnroll
	}
	return stage, s.startSession(ctx, w, u.ID, stage)
}

// VerifySecondFactor accepts an authenticator code, an emailed code or a
// recovery code for a session waiting at StageMFA.
func (s *Service) VerifySecondFactor(w http.ResponseWriter, r *http.Request, code string) error {
	ctx := r.Context()
	sess := SessionFrom(ctx)
	if sess == nil || sess.Stage != StageMFA {
		return ErrInvalidCode
	}
	u, c, err := s.userWithCredentials(ctx, "u.id = $1", sess.User.ID)
	if err != nil {
		return err
	}
	if c.lockedUntil != nil && c.lockedUntil.After(s.now()) {
		return ErrLocked
	}
	ok, method, err := s.checkCode(ctx, u.ID, c, normalizeCode(code))
	if err != nil {
		return err
	}
	if !ok {
		s.recordFailure(ctx, u.ID, ClientIP(r))
		return ErrInvalidCode
	}
	s.audit(ctx, &u.ID, "login.password+"+method, "", ClientIP(r))
	return s.upgrade(ctx, w, sess, StageFull)
}

func (s *Service) checkCode(ctx context.Context, userID int64, c *credentials, code string) (bool, string, error) {
	if len(code) == 6 && strings.Trim(code, "0123456789") == "" {
		if c.totpSecret != nil {
			secret, _, err := s.keys.Open(*c.totpSecret, totpAAD(userID))
			if err != nil {
				return false, "", err
			}
			if step, ok := matchTOTP(string(secret), code, s.now()); ok {
				tag, err := s.pool.Exec(ctx,
					"UPDATE users SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2", userID, step)
				if err != nil {
					return false, "", err
				}
				if tag.RowsAffected() == 1 {
					return true, "totp", nil
				}
			}
		}
		return s.checkEmailCode(ctx, userID, code)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE recovery_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, hashRecoveryCode(code))
	if err != nil {
		return false, "", err
	}
	return tag.RowsAffected() == 1, "recovery", nil
}

func (s *Service) checkEmailCode(ctx context.Context, userID int64, code string) (bool, string, error) {
	var mac []byte
	err := s.pool.QueryRow(ctx, `UPDATE email_codes SET attempts = attempts + 1
		WHERE user_id = $1 AND expires_at > now() AND attempts < $2
		RETURNING code_mac`, userID, maxCodeAttempts).Scan(&mac)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if !equalMAC(mac, s.emailCodeMAC(userID, code)) {
		return false, "", nil
	}
	_, err = s.pool.Exec(ctx, "DELETE FROM email_codes WHERE user_id = $1", userID)
	return err == nil, "email", err
}

// SendEmailCode mails a one-time code to the user of a StageMFA session.
func (s *Service) SendEmailCode(r *http.Request, subject, bodyFormat string) error {
	ctx := r.Context()
	sess := SessionFrom(ctx)
	if sess == nil || sess.Stage != StageMFA || !s.EmailEnabled() {
		return ErrInvalidCode
	}
	code := randomDigits(6)
	tag, err := s.pool.Exec(ctx, `INSERT INTO email_codes (user_id, code_mac, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET code_mac = $2, expires_at = $3, sent_at = now(), attempts = 0
		WHERE email_codes.sent_at < now() - $4::interval`,
		sess.User.ID, s.emailCodeMAC(sess.User.ID, code), s.now().Add(emailCodeTTL),
		fmt.Sprintf("%d seconds", int(emailCodeInterval.Seconds())))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTooSoon
	}
	return s.mailer.SendEmail(subject, fmt.Sprintf(bodyFormat, code), []string{sess.User.Email}, nil)
}

type Enrollment struct {
	Secret string
	QR     string
}

// StartEnrollment returns the authenticator secret for a StageEnroll session,
// reusing the pending one so reloading the page does not invalidate a scan.
func (s *Service) StartEnrollment(ctx context.Context) (*Enrollment, error) {
	sess := SessionFrom(ctx)
	if sess == nil || sess.Stage != StageEnroll {
		return nil, ErrNotFound
	}
	_, c, err := s.userWithCredentials(ctx, "u.id = $1", sess.User.ID)
	if err != nil {
		return nil, err
	}
	var key *otp.Key
	if c.totpSecret != nil {
		secret, _, err := s.keys.Open(*c.totpSecret, totpAAD(sess.User.ID))
		if err != nil {
			return nil, err
		}
		if key, err = otpKey(totpIssuer, sess.User.Email, string(secret)); err != nil {
			return nil, err
		}
	} else {
		if key, err = newTOTPKey(totpIssuer, sess.User.Email); err != nil {
			return nil, err
		}
		sealed, err := s.keys.Seal([]byte(key.Secret()), totpAAD(sess.User.ID))
		if err != nil {
			return nil, err
		}
		if _, err := s.pool.Exec(ctx, "UPDATE users SET totp_secret_enc = $2 WHERE id = $1",
			sess.User.ID, sealed); err != nil {
			return nil, err
		}
	}
	qr, err := totpQR(key)
	if err != nil {
		return nil, err
	}
	return &Enrollment{Secret: key.Secret(), QR: qr}, nil
}

// ConfirmEnrollment activates the pending authenticator once the user proves
// it works, issues recovery codes and completes sign-in.
func (s *Service) ConfirmEnrollment(w http.ResponseWriter, r *http.Request, code string) ([]string, error) {
	ctx := r.Context()
	sess := SessionFrom(ctx)
	if sess == nil || sess.Stage != StageEnroll {
		return nil, ErrInvalidCode
	}
	_, c, err := s.userWithCredentials(ctx, "u.id = $1", sess.User.ID)
	if err != nil {
		return nil, err
	}
	if c.totpSecret == nil {
		return nil, ErrInvalidCode
	}
	secret, _, err := s.keys.Open(*c.totpSecret, totpAAD(sess.User.ID))
	if err != nil {
		return nil, err
	}
	step, ok := matchTOTP(string(secret), normalizeCode(code), s.now())
	if !ok {
		return nil, ErrInvalidCode
	}
	codes := make([]string, 10)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET totp_confirmed_at = now(), totp_last_step = $2
			WHERE id = $1`, sess.User.ID, step); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM recovery_codes WHERE user_id = $1", sess.User.ID); err != nil {
			return err
		}
		for i := range codes {
			codes[i] = newRecoveryCode()
			if _, err := tx.Exec(ctx, "INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2)",
				sess.User.ID, hashRecoveryCode(codes[i])); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, &sess.User.ID, "mfa.enrolled", "", ClientIP(r))
	return codes, s.upgrade(ctx, w, sess, StageFull)
}
