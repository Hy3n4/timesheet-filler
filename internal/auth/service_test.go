package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"timesheet-filler/internal/db"
	"timesheet-filler/internal/secretbox"
)

// These tests need PostgreSQL: TEST_DATABASE_URL=postgres://... go test ./internal/auth/
// Every test truncates the auth tables, so point it at a throwaway database.

type fakeMailer struct{ bodies []string }

func (m *fakeMailer) IsConfigured() bool { return true }

func (m *fakeMailer) SendEmail(_, body string, _, _ []string) error {
	m.bodies = append(m.bodies, body)
	return nil
}

func testService(t *testing.T) (*Service, *fakeMailer) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, db.Migrate(ctx, pool))
	_, err = pool.Exec(ctx, `TRUNCATE users, sessions, invites, oauth_states, recovery_codes, email_codes, audit_log
		RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	keys, err := secretbox.ParseKeyring("v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	require.NoError(t, err)
	mailer := &fakeMailer{}
	svc := NewService(pool, keys, Config{
		BaseURL: "http://test", AdminEmails: []string{"boss@example.com"},
		SessionTTL: time.Hour, SessionIdle: time.Hour,
	}, mailer)
	return svc, mailer
}

func seedUser(t *testing.T, s *Service, email string, role Role, password string) *User {
	t.Helper()
	var hash, sub *string
	if password != "" {
		h, err := HashPassword(password)
		require.NoError(t, err)
		hash = &h
	} else {
		sub = &email
	}
	u, err := scanUser(s.pool.QueryRow(context.Background(), `INSERT INTO users AS u (email, role, password_hash, google_sub)
		VALUES ($1, $2, $3, $4) RETURNING `+userColumns, email, role, hash, sub))
	require.NoError(t, err)
	return u
}

func asUser(u *User) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, &Session{User: u, Stage: StageFull}))
}

// browser keeps cookies between calls that run through Service.Middleware.
type browser struct {
	svc     *Service
	cookies map[string]*http.Cookie
}

func newBrowser(s *Service) *browser { return &browser{svc: s, cookies: map[string]*http.Cookie{}} }

func (b *browser) call(fn func(w http.ResponseWriter, r *http.Request)) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	b.svc.Middleware(http.HandlerFunc(fn)).ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
}

func (b *browser) session() *Session {
	var s *Session
	b.call(func(_ http.ResponseWriter, r *http.Request) { s = SessionFrom(r.Context()) })
	return s
}

func (b *browser) login(email, password string) (Stage, error) {
	var stage Stage
	var err error
	b.call(func(w http.ResponseWriter, r *http.Request) { stage, err = b.svc.PasswordLogin(w, r, email, password) })
	return stage, err
}

func (b *browser) secondFactor(code string) error {
	var err error
	b.call(func(w http.ResponseWriter, r *http.Request) { err = b.svc.VerifySecondFactor(w, r, code) })
	return err
}

func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	require.NoError(t, err)
	return c
}

func TestInviteToPasswordAccountWithAuthenticator(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	admin := seedUser(t, svc, "boss@example.com", RoleAdmin, "")

	token, err := svc.CreateInvite(asUser(admin), "Coach@Example.com", RoleMember, " Chadima Patrik ")
	require.NoError(t, err)
	inv, err := svc.InviteByToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "coach@example.com", inv.Email)
	assert.Equal(t, "Chadima Patrik", inv.EosName)

	b := newBrowser(svc)
	b.call(func(w http.ResponseWriter, r *http.Request) {
		err = svc.AcceptInvitePassword(w, r, token, "Patrik", "short")
	})
	assert.ErrorIs(t, err, ErrWeakPassword)
	b.call(func(w http.ResponseWriter, r *http.Request) {
		err = svc.AcceptInvitePassword(w, r, token, "Patrik", "a long enough password")
	})
	require.NoError(t, err)
	assert.Equal(t, StageEnroll, b.session().Stage)

	b.call(func(w http.ResponseWriter, r *http.Request) {
		err = svc.AcceptInvitePassword(w, r, token, "Again", "a long enough password")
	})
	assert.ErrorIs(t, err, ErrInviteInvalid, "an invite works once")

	var first, second *Enrollment
	b.call(func(_ http.ResponseWriter, r *http.Request) { first, err = svc.StartEnrollment(r.Context()) })
	require.NoError(t, err)
	b.call(func(_ http.ResponseWriter, r *http.Request) { second, err = svc.StartEnrollment(r.Context()) })
	require.NoError(t, err)
	assert.Equal(t, first.Secret, second.Secret, "reloading keeps the scanned secret")
	assert.Contains(t, first.QR, "data:image/png;base64,")

	var stored string
	require.NoError(t, svc.pool.QueryRow(ctx, "SELECT totp_secret_enc FROM users WHERE email = 'coach@example.com'").Scan(&stored))
	assert.NotContains(t, stored, first.Secret, "the secret is sealed at rest")

	var codes []string
	b.call(func(w http.ResponseWriter, r *http.Request) { codes, err = svc.ConfirmEnrollment(w, r, "000000") })
	assert.ErrorIs(t, err, ErrInvalidCode)
	b.call(func(w http.ResponseWriter, r *http.Request) {
		codes, err = svc.ConfirmEnrollment(w, r, totpCode(t, first.Secret, time.Now()))
	})
	require.NoError(t, err)
	assert.Len(t, codes, 10)
	sess := b.session()
	assert.Equal(t, StageFull, sess.Stage)
	assert.Equal(t, "Chadima Patrik", sess.User.EosName)
	assert.True(t, sess.User.HasTOTP)

	// A new sign-in needs the second factor; codes cannot be replayed.
	b2 := newBrowser(svc)
	stage, err := b2.login("COACH@example.com", "a long enough password")
	require.NoError(t, err)
	assert.Equal(t, StageMFA, stage)
	assert.Nil(t, UserFrom(contextOf(b2)))
	next := totpCode(t, first.Secret, time.Now().Add(totpPeriod*time.Second))
	require.NoError(t, b2.secondFactor(next))
	assert.Equal(t, StageFull, b2.session().Stage)

	b3 := newBrowser(svc)
	_, err = b3.login("coach@example.com", "a long enough password")
	require.NoError(t, err)
	assert.ErrorIs(t, b3.secondFactor(next), ErrInvalidCode, "replayed code")
	require.NoError(t, b3.secondFactor(codes[0]))

	b4 := newBrowser(svc)
	_, err = b4.login("coach@example.com", "a long enough password")
	require.NoError(t, err)
	assert.ErrorIs(t, b4.secondFactor(codes[0]), ErrInvalidCode, "recovery codes work once")
}

func contextOf(b *browser) context.Context {
	var ctx context.Context
	b.call(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })
	return ctx
}

func TestPasswordLoginLocksAfterFailures(t *testing.T) {
	svc, _ := testService(t)
	seedUser(t, svc, "coach@example.com", RoleMember, "the right password")
	b := newBrowser(svc)

	_, err := b.login("nobody@example.com", "the right password")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
	for range maxFailedLogins {
		_, err = b.login("coach@example.com", "a wrong password")
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	}
	_, err = b.login("coach@example.com", "the right password")
	assert.ErrorIs(t, err, ErrLocked)
	assert.Nil(t, b.session())
}

func TestPasswordUserWithoutAuthenticatorMustEnroll(t *testing.T) {
	svc, _ := testService(t)
	seedUser(t, svc, "coach@example.com", RoleMember, "the right password")
	b := newBrowser(svc)
	stage, err := b.login("coach@example.com", "the right password")
	require.NoError(t, err)
	assert.Equal(t, StageEnroll, stage)
	assert.ErrorIs(t, b.secondFactor("123456"), ErrInvalidCode)
}

func TestEmailCode(t *testing.T) {
	svc, mailer := testService(t)
	u := seedUser(t, svc, "coach@example.com", RoleMember, "the right password")
	_, err := svc.pool.Exec(context.Background(), "UPDATE users SET totp_confirmed_at = now() WHERE id = $1", u.ID)
	require.NoError(t, err)

	b := newBrowser(svc)
	_, err = b.login("coach@example.com", "the right password")
	require.NoError(t, err)
	b.call(func(_ http.ResponseWriter, r *http.Request) { err = svc.SendEmailCode(r, "subject", "code %s") })
	require.NoError(t, err)
	b.call(func(_ http.ResponseWriter, r *http.Request) { err = svc.SendEmailCode(r, "subject", "code %s") })
	assert.ErrorIs(t, err, ErrTooSoon)
	require.Len(t, mailer.bodies, 1)
	code := regexp.MustCompile(`\d{6}`).FindString(mailer.bodies[0])

	assert.ErrorIs(t, b.secondFactor("000000"), ErrInvalidCode)
	require.NoError(t, b.secondFactor(code))
	assert.Equal(t, StageFull, b.session().Stage)
}

func TestInviteRules(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	admin := seedUser(t, svc, "boss@example.com", RoleAdmin, "")

	_, err := svc.CreateInvite(asUser(admin), "Boss@example.com", RoleMember, "")
	assert.ErrorIs(t, err, ErrUserExists)
	_, err = svc.CreateInvite(asUser(admin), "Coach <coach@example.com>", RoleMember, "")
	assert.ErrorIs(t, err, ErrInvalidEmail)

	old, err := svc.CreateInvite(asUser(admin), "coach@example.com", RoleMember, "")
	require.NoError(t, err)
	newer, err := svc.CreateInvite(asUser(admin), "coach@example.com", RoleAdmin, "")
	require.NoError(t, err)
	_, err = svc.InviteByToken(ctx, old)
	assert.ErrorIs(t, err, ErrInviteInvalid, "a new invite replaces the old one")

	inv, err := svc.InviteByToken(ctx, newer)
	require.NoError(t, err)
	renewed, _, err := svc.RenewInvite(asUser(admin), inv.ID)
	require.NoError(t, err)
	_, err = svc.InviteByToken(ctx, newer)
	assert.ErrorIs(t, err, ErrInviteInvalid)
	require.NoError(t, svc.RevokeInvite(asUser(admin), inv.ID))
	_, err = svc.InviteByToken(ctx, renewed)
	assert.ErrorIs(t, err, ErrInviteInvalid)

	pending, err := svc.PendingInvites(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestAdminGuards(t *testing.T) {
	svc, _ := testService(t)
	admin := seedUser(t, svc, "boss@example.com", RoleAdmin, "")
	other := seedUser(t, svc, "other@example.com", RoleAdmin, "")
	member := seedUser(t, svc, "coach@example.com", RoleMember, "the right password")

	assert.ErrorIs(t, svc.SetDisabled(asUser(admin), admin.ID, true), ErrSelf)
	assert.ErrorIs(t, svc.DeleteUser(asUser(admin), admin.ID), ErrSelf)
	assert.ErrorIs(t, svc.UpdateUser(asUser(admin), admin.ID, RoleMember, ""), ErrSelf)
	require.NoError(t, svc.UpdateUser(asUser(admin), admin.ID, RoleAdmin, "Šéf Hlavní"))

	b := newBrowser(svc)
	_, err := b.login("coach@example.com", "the right password")
	require.NoError(t, err)
	require.NotNil(t, b.session())
	require.NoError(t, svc.SetDisabled(asUser(admin), member.ID, true))
	assert.Nil(t, b.session(), "disabling drops sessions")
	_, err = b.login("coach@example.com", "the right password")
	assert.ErrorIs(t, err, ErrDisabled)

	require.NoError(t, svc.SetDisabled(asUser(admin), other.ID, true))
	assert.ErrorIs(t, svc.DeleteUser(asUser(other), admin.ID), ErrLastAdmin)

	users, err := svc.Users(context.Background())
	require.NoError(t, err)
	assert.Len(t, users, 3)
	audit, err := svc.AuditLog(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, "user.disabled", audit[0].Action)
	assert.Equal(t, "boss@example.com", audit[0].Actor)
}

func TestGoogleAccounts(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	_, err := svc.googleUser(ctx, &googleClaims{Subject: "1", Email: "someone@gmail.com", EmailVerified: true})
	assert.ErrorIs(t, err, ErrNoAccount, "no account without an invite")

	boss, err := svc.googleUser(ctx, &googleClaims{Subject: "2", Email: "boss@example.com", Name: "Boss"})
	require.NoError(t, err)
	assert.True(t, boss.IsAdmin(), "ADMIN_EMAILS bootstrap")
	again, err := svc.googleUser(ctx, &googleClaims{Subject: "2", Email: "boss@example.com"})
	require.NoError(t, err)
	assert.Equal(t, boss.ID, again.ID)

	token, err := svc.CreateInvite(asUser(boss), "coach@gmail.com", RoleMember, "Coach Name")
	require.NoError(t, err)
	inv, err := svc.InviteByToken(ctx, token)
	require.NoError(t, err)
	_, err = svc.acceptInviteGoogle(ctx, inv.ID, &googleClaims{Subject: "3", Email: "other@gmail.com"})
	assert.ErrorIs(t, err, ErrEmailMismatch)
	coach, err := svc.acceptInviteGoogle(ctx, inv.ID, &googleClaims{Subject: "3", Email: "coach@gmail.com"})
	require.NoError(t, err)
	assert.Equal(t, "Coach Name", coach.EosName)
	assert.True(t, coach.HasGoogle)
	assert.False(t, coach.IsAdmin())
}
