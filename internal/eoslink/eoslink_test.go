package eoslink

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"timesheet-filler/internal/db"
	"timesheet-filler/internal/eos"
	"timesheet-filler/internal/secretbox"
)

// Needs PostgreSQL: TEST_DATABASE_URL=postgres://... go test ./internal/eoslink/
// The auth and eoslink tests truncate the same tables, so run packages with -p 1.

func jwt(mid int64, exp time.Time) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]any{"mid": mid, "exp": exp.Unix()})
	return enc([]byte(`{}`)) + "." + enc(payload) + ".s"
}

// fakeEOS hands out refresh tokens "r1", "r2", ... and access tokens that
// live accessTTL. Revoking makes every refresh and list call answer 401.
type fakeEOS struct {
	mu        sync.Mutex
	refreshes int
	deleted   []string
	accessTTL time.Duration
	revoked   bool
	failList  int
	next      int
}

func (f *fakeEOS) pair(w http.ResponseWriter, nested bool) {
	f.next++
	access := jwt(329, time.Now().Add(f.accessTTL))
	refresh := fmt.Sprintf("r%d", f.next)
	w.WriteHeader(http.StatusCreated)
	if nested {
		fmt.Fprintf(w, `{"access_token":{"token":%q,"session":{"id":900}},"refresh_token":{"token":%q,"session":{"id":%d}}}`,
			access, refresh, 100+f.next)
		return
	}
	fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q}`, access, refresh)
}

func (f *fakeEOS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.URL.Path == "/auth/sessions" && r.Method == http.MethodPost:
		if body["password"] != "right" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(body["email"].(string), "otp") {
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"authentication_code":"code-1","provisioning_qr_code":null}`)
			return
		}
		f.pair(w, true)
	case r.URL.Path == "/auth/otp":
		if body["authentication_code"] != "code-1" || body["otp"] != "123456" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		f.pair(w, true)
	case r.URL.Path == "/auth/tokens":
		f.refreshes++
		if f.revoked {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.pair(w, false)
	case strings.HasPrefix(r.URL.Path, "/auth/sessions/") && r.Method == http.MethodDelete:
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/auth/sessions/"))
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/management/event-attendances":
		if f.revoked || f.failList > 0 {
			f.failList--
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":1,"event_id":10,"member_id":329,"evaluated_attendance":true,"stated_at":"2026-10-15 18:00:00"}]}`)
	case r.URL.Path == "/management/events":
		fmt.Fprint(w, `{"data":[{"id":10,"name":"U11 - hala","event_type_id":1,"purpose":"none","range":{"from":"2026-10-15 16:00:00","to":"2026-10-15 17:30:00"},"team_ids":[85]}]}`)
	case r.URL.Path == "/event-types":
		fmt.Fprint(w, `{"data":[{"id":1,"name":"Trénink","has_attendance":true}]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func setup(t *testing.T) (*Service, *fakeEOS, int64) {
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
	_, err = pool.Exec(ctx, `TRUNCATE users, sessions, invites, oauth_states, recovery_codes, email_codes, audit_log,
		eos_links, eos_pending_logins RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	var userID int64
	require.NoError(t, pool.QueryRow(ctx,
		"INSERT INTO users (email, role, google_sub) VALUES ('coach@example.com', 'member', 'g1') RETURNING id").Scan(&userID))

	fake := &fakeEOS{accessTTL: time.Hour}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	keys, err := secretbox.ParseKeyring("v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	require.NoError(t, err)
	return NewService(pool, keys, eos.NewClient(srv.URL)), fake, userID
}

func TestLinkStoresSealedTokensAndReusesTheAccessToken(t *testing.T) {
	s, fake, user := setup(t)
	ctx := context.Background()

	_, err := s.Link(ctx, user, "coach@example.com", "wrong", "")
	assert.ErrorIs(t, err, eos.ErrInvalidCredentials)
	needOTP, err := s.Link(ctx, user, "coach@example.com", "right", "")
	require.NoError(t, err)
	assert.False(t, needOTP)

	link, pending, err := s.Get(ctx, user)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Equal(t, int64(329), link.MemberID)
	assert.Equal(t, StatusActive, link.Status)

	var refreshEnc string
	require.NoError(t, s.pool.QueryRow(ctx, "SELECT refresh_enc FROM eos_links WHERE user_id = $1", user).Scan(&refreshEnc))
	assert.True(t, strings.HasPrefix(refreshEnc, "v1:"))
	assert.NotContains(t, refreshEnc, "r1")

	token, member, err := s.AccessToken(ctx, user)
	require.NoError(t, err)
	assert.Equal(t, int64(329), member)
	assert.NotEmpty(t, token)
	assert.Zero(t, fake.refreshes, "a fresh access token is reused")
}

func TestExpiredAccessTokenIsRefreshedOnceForConcurrentRequests(t *testing.T) {
	s, fake, user := setup(t)
	ctx := context.Background()
	fake.accessTTL = time.Minute // inside the refresh margin
	_, err := s.Link(ctx, user, "coach@example.com", "right", "")
	require.NoError(t, err)
	fake.accessTTL = time.Hour

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.AccessToken(ctx, user)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, fake.refreshes)

	link, _, err := s.Get(ctx, user)
	require.NoError(t, err)
	assert.NotNil(t, link.RefreshedAt)
}

func TestRevokedSessionNeedsANewLogin(t *testing.T) {
	s, fake, user := setup(t)
	ctx := context.Background()
	_, err := s.Link(ctx, user, "coach@example.com", "right", "")
	require.NoError(t, err)
	fake.revoked = true

	_, err = s.MonthRows(ctx, user, 2026, 10)
	assert.ErrorIs(t, err, ErrReauthRequired)
	link, _, err := s.Get(ctx, user)
	require.NoError(t, err)
	assert.Equal(t, StatusReauth, link.Status)

	calls := fake.refreshes
	_, _, err = s.AccessToken(ctx, user)
	assert.ErrorIs(t, err, ErrReauthRequired)
	assert.Equal(t, calls, fake.refreshes, "no EOS call once a new login is required")

	fake.revoked = false
	_, err = s.Link(ctx, user, "coach@example.com", "right", "")
	require.NoError(t, err)
	_, _, err = s.AccessToken(ctx, user)
	assert.NoError(t, err)
}

func TestTwoFactorLogin(t *testing.T) {
	s, _, user := setup(t)
	ctx := context.Background()
	assert.ErrorIs(t, s.CompleteOTP(ctx, user, "123456", ""), ErrNoPendingLogin)

	needOTP, err := s.Link(ctx, user, "otp-coach@example.com", "right", "")
	require.NoError(t, err)
	assert.True(t, needOTP)
	_, pending, err := s.Get(ctx, user)
	require.NoError(t, err)
	assert.True(t, pending)

	assert.ErrorIs(t, s.CompleteOTP(ctx, user, "000000", ""), eos.ErrInvalidCredentials)
	require.NoError(t, s.CompleteOTP(ctx, user, "123456", ""))
	link, pending, err := s.Get(ctx, user)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Equal(t, StatusActive, link.Status)
}

func TestUnlinkEndsTheEOSSessionAndRelinkReplacesIt(t *testing.T) {
	s, fake, user := setup(t)
	ctx := context.Background()
	_, err := s.Link(ctx, user, "coach@example.com", "right", "")
	require.NoError(t, err)
	_, err = s.Link(ctx, user, "coach@example.com", "right", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"101"}, fake.deleted, "relinking ends the previous session")

	ended, err := s.Unlink(ctx, user, "")
	require.NoError(t, err)
	assert.True(t, ended)
	assert.Equal(t, []string{"101", "102"}, fake.deleted)
	link, _, err := s.Get(ctx, user)
	require.NoError(t, err)
	assert.Nil(t, link)
	_, err = s.Unlink(ctx, user, "")
	assert.ErrorIs(t, err, ErrNotLinked)
}

func TestMonthRowsRetriesOnceWithAFreshToken(t *testing.T) {
	s, fake, user := setup(t)
	ctx := context.Background()
	_, err := s.Link(ctx, user, "coach@example.com", "right", "")
	require.NoError(t, err)
	fake.failList = 1

	rows, err := s.MonthRows(ctx, user, 2026, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "2026-10-15", rows[0].Date)
	assert.Equal(t, 1, fake.refreshes)

	_, err = s.MonthRows(ctx, user+1, 2026, 10)
	assert.ErrorIs(t, err, ErrNotLinked)
}

func TestFailedLinksAreCapped(t *testing.T) {
	s, _, user := setup(t)
	ctx := context.Background()
	for range maxFailedLinks {
		_, err := s.Link(ctx, user, "coach@example.com", "wrong", "")
		assert.ErrorIs(t, err, eos.ErrInvalidCredentials)
	}
	_, err := s.Link(ctx, user, "coach@example.com", "right", "")
	assert.ErrorIs(t, err, ErrTooManyTries)
}
