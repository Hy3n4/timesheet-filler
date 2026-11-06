package eos

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeJWT builds an unsigned token with the claims the client reads.
func fakeJWT(mid int64, exp time.Time) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]any{"mid": mid, "exp": exp.Unix(), "typ": "access"})
	return enc([]byte(`{"alg":"HS256"}`)) + "." + enc(payload) + ".sig"
}

func TestLoginReturnsTokensFromTheNestedShape(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/auth/sessions", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "long", body["lifespan"])
		assert.Nil(t, body["member_id"])
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"access_token":{"token":%q,"session":{"id":2}},"refresh_token":{"token":"r1","session":{"id":1}}}`,
			fakeJWT(329, exp))
	}))
	defer srv.Close()

	res, err := NewClient(srv.URL).Login(context.Background(), "coach@example.com", "pw")
	require.NoError(t, err)
	require.NotNil(t, res.Tokens)
	assert.Equal(t, "r1", res.Tokens.Refresh)
	assert.Equal(t, int64(1), res.Tokens.RefreshSessionID)
	assert.Equal(t, int64(329), res.Tokens.MemberID)
	assert.True(t, exp.Equal(res.Tokens.AccessExpires))
}

func TestLoginWithTwoFactor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/sessions":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"authentication_code":"abc","expires_at":"2026-11-06 21:00:00","provisioning_qr_code":null}`)
		case "/auth/otp":
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if body["authentication_code"] != "abc" || body["otp"] != "123456" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"access_token":{"token":%q},"refresh_token":{"token":"r","session":{"id":7}}}`,
				fakeJWT(5, time.Now().Add(time.Hour)))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	res, err := c.Login(context.Background(), "a@b.cz", "pw")
	require.NoError(t, err)
	assert.Nil(t, res.Tokens)
	assert.Equal(t, "abc", res.AuthenticationCode)
	_, err = c.CompleteOTP(context.Background(), "abc", "000000")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
	tokens, err := c.CompleteOTP(context.Background(), "abc", "123456")
	require.NoError(t, err)
	assert.Equal(t, int64(7), tokens.RefreshSessionID)
}

func TestLoginErrors(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusUnauthorized: ErrInvalidCredentials,
		http.StatusLocked:       ErrLocked,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		_, err := NewClient(srv.URL).Login(context.Background(), "a@b.cz", "pw")
		assert.ErrorIs(t, err, want)
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"authentication_code":"abc","provisioning_qr_code":{"data":"otpauth://..."}}`)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL).Login(context.Background(), "a@b.cz", "pw")
	assert.ErrorIs(t, err, ErrOTPSetupRequired)
}

func TestRefreshReadsTheFlatShapeAndMapsUnauthorized(t *testing.T) {
	ok := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer r1", r.Header.Get("Authorization"))
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"type":"ExpiredRefreshToken","message":"expired"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"r2"}`, fakeJWT(329, time.Now().Add(time.Hour)))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	tokens, err := c.Refresh(context.Background(), "r1", 329)
	require.NoError(t, err)
	assert.Equal(t, "r2", tokens.Refresh)
	assert.Zero(t, tokens.RefreshSessionID)
	ok = false
	_, err = c.Refresh(context.Background(), "r1", 329)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestFetchMonthQueries(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		q := r.URL.Query()
		assert.Equal(t, "0", q.Get("paginated"))
		assert.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case "/management/event-attendances":
			raw, _ := base64.StdEncoding.DecodeString(q.Get("filters"))
			assert.JSONEq(t, `[{"f":["member_id"],"o":"eq","v":329}]`, string(raw))
			fmt.Fprint(w, `{"data":[{"id":1,"event_id":10,"member_id":329,"evaluated_attendance":true,"stated_at":null}]}`)
		case "/management/events":
			raw, _ := base64.StdEncoding.DecodeString(q.Get("filters"))
			assert.JSONEq(t, `[{"f":["range_from"],"o":"gte","v":"2026-12-01 00:00:00"},{"f":["range_from"],"o":"lt","v":"2027-01-01 00:00:00"}]`, string(raw))
			assert.Equal(t, "team_ids,related_team_ids", q.Get("with"))
			fmt.Fprint(w, `{"data":[{"id":10,"name":"Trénink","event_type_id":1,"purpose":"none","range":{"from":"2026-12-02 16:00:00","to":"2026-12-02 17:30:00"},"team_ids":[85]}]}`)
		case "/event-types":
			fmt.Fprint(w, `{"data":[{"id":1,"name":"Trénink","has_attendance":true}]}`)
		default:
			t.Errorf("unexpected EOS call %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	m, err := NewClient(srv.URL).FetchMonth(context.Background(), "a", 329, 2026, 12)
	require.NoError(t, err)
	assert.Equal(t, []string{"/management/event-attendances", "/management/events", "/event-types"}, paths)
	rows, err := BuildRows(*m)
	require.NoError(t, err)
	assert.Len(t, TableRows(rows), 1)
}

func TestFetchMonthForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"Forbidden","message":"no access"}`)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL).FetchMonth(context.Background(), "a", 1, 2026, 1)
	assert.ErrorIs(t, err, ErrForbidden)
}
