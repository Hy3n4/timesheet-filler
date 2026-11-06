package eos

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("eos: invalid credentials")
	ErrLocked             = errors.New("eos: account locked")
	ErrOTPSetupRequired   = errors.New("eos: two-factor setup required in EOS first")
	// ErrUnauthorized means EOS rejected the token: the session is gone.
	ErrUnauthorized = errors.New("eos: unauthorized")
	ErrForbidden    = errors.New("eos: forbidden")
)

// APIError is any other unexpected EOS response.
type APIError struct {
	Path    string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("eos: %s: HTTP %d %s", e.Path, e.Status, e.Message)
}

type Client struct {
	base string
	http *http.Client
}

func NewClient(baseURL string) *Client {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	return &Client{base: baseURL, http: &http.Client{Timeout: 30 * time.Second}}
}

// Tokens is the result of a login or refresh. RefreshSessionID is only known
// after a login; it names the EOS session that unlinking deletes.
type Tokens struct {
	Access           string
	Refresh          string
	RefreshSessionID int64
	AccessExpires    time.Time
	MemberID         int64
}

// LoginResult holds either tokens or the code that continues with CompleteOTP.
type LoginResult struct {
	Tokens             *Tokens
	AuthenticationCode string
}

func (c *Client) do(ctx context.Context, method, path, bearer string, body any, params url.Values) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	u := c.base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "timesheet-filler")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("eos: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, raw, err
}

func apiError(path string, status int, raw []byte) error {
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &e)
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, e.Message)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrForbidden, e.Message)
	}
	return &APIError{Path: path, Status: status, Message: e.Message}
}

// tokenEntry accepts both shapes EOS uses: {"token": "...", "session": {...}}
// from logins and a plain string from refreshes.
type tokenEntry struct {
	Token     string
	SessionID int64
}

func (t *tokenEntry) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &t.Token)
	}
	var v struct {
		Token   string `json:"token"`
		Session struct {
			ID int64 `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	t.Token, t.SessionID = v.Token, v.Session.ID
	return nil
}

func parseTokens(raw []byte) (*Tokens, error) {
	var pair struct {
		Access  *tokenEntry `json:"access_token"`
		Refresh *tokenEntry `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &pair); err != nil {
		return nil, fmt.Errorf("eos: token response: %w", err)
	}
	if pair.Access == nil || pair.Refresh == nil || pair.Access.Token == "" || pair.Refresh.Token == "" {
		return nil, errors.New("eos: token response without tokens")
	}
	claims, err := accessClaims(pair.Access.Token)
	if err != nil {
		return nil, err
	}
	return &Tokens{
		Access:           pair.Access.Token,
		Refresh:          pair.Refresh.Token,
		RefreshSessionID: pair.Refresh.SessionID,
		AccessExpires:    time.Unix(claims.Exp, 0),
		MemberID:         claims.Mid,
	}, nil
}

type claims struct {
	Exp int64 `json:"exp"`
	Mid int64 `json:"mid"`
}

// accessClaims reads the expiry and member id of an EOS access JWT. The token
// comes straight from EOS over TLS, so its signature is not checked here.
func accessClaims(token string) (*claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("eos: access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("eos: access token payload: %w", err)
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil || c.Exp == 0 || c.Mid == 0 {
		return nil, errors.New("eos: access token without exp or mid")
	}
	return &c, nil
}

// Login starts a long-lived session for the EOS user. EOS answers 202 with an
// authentication code when the account has two-factor authentication.
func (c *Client) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	const path = "auth/sessions"
	status, raw, err := c.do(ctx, http.MethodPost, path, "", map[string]any{
		"email": email, "password": password, "member_id": nil, "lifespan": "long",
	}, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusAccepted:
		var otp struct {
			Code string          `json:"authentication_code"`
			QR   json.RawMessage `json:"provisioning_qr_code"`
		}
		if err := json.Unmarshal(raw, &otp); err != nil || otp.Code == "" {
			return nil, &APIError{Path: path, Status: status, Message: "no authentication code"}
		}
		if len(otp.QR) > 0 && string(otp.QR) != "null" {
			return nil, ErrOTPSetupRequired
		}
		return &LoginResult{AuthenticationCode: otp.Code}, nil
	case status >= 200 && status < 300:
		t, err := parseTokens(raw)
		if err != nil {
			return nil, err
		}
		return &LoginResult{Tokens: t}, nil
	case status == http.StatusUnauthorized || status == http.StatusUnprocessableEntity:
		return nil, ErrInvalidCredentials
	case status == http.StatusLocked:
		return nil, ErrLocked
	}
	return nil, apiError(path, status, raw)
}

// CompleteOTP finishes a login that needed the EOS two-factor code.
func (c *Client) CompleteOTP(ctx context.Context, authenticationCode, otp string) (*Tokens, error) {
	const path = "auth/otp"
	status, raw, err := c.do(ctx, http.MethodPost, path, "", map[string]string{
		"authentication_code": authenticationCode, "otp": otp,
	}, nil)
	if err != nil {
		return nil, err
	}
	if status >= 200 && status < 300 {
		return parseTokens(raw)
	}
	if status == http.StatusUnauthorized || status == http.StatusUnprocessableEntity {
		return nil, ErrInvalidCredentials
	}
	return nil, apiError(path, status, raw)
}

// Refresh exchanges the refresh token for a new pair for memberID.
func (c *Client) Refresh(ctx context.Context, refresh string, memberID int64) (*Tokens, error) {
	const path = "auth/tokens"
	status, raw, err := c.do(ctx, http.MethodPost, path, refresh, map[string]int64{"member_id": memberID}, nil)
	if err != nil {
		return nil, err
	}
	if status >= 200 && status < 300 {
		return parseTokens(raw)
	}
	return nil, apiError(path, status, raw)
}

// DeleteSession ends an EOS session, trying the access token first and the
// refresh token second.
func (c *Client) DeleteSession(ctx context.Context, sessionID int64, bearers ...string) error {
	path := fmt.Sprintf("auth/sessions/%d", sessionID)
	var last error
	for _, bearer := range bearers {
		if bearer == "" {
			continue
		}
		status, raw, err := c.do(ctx, http.MethodDelete, path, bearer, nil, nil)
		if err != nil {
			last = err
			continue
		}
		if status < 300 || status == http.StatusNotFound {
			return nil
		}
		last = apiError(path, status, raw)
	}
	return last
}

type filter struct {
	F []string `json:"f"`
	O string   `json:"o"`
	V any      `json:"v"`
}

func encodeFilters(fs ...filter) string {
	b, _ := json.Marshal(fs)
	return base64.StdEncoding.EncodeToString(b)
}

func (c *Client) list(ctx context.Context, bearer, path string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("paginated", "0")
	status, raw, err := c.do(ctx, http.MethodGet, path, bearer, nil, params)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return apiError(path, status, raw)
	}
	envelope := struct {
		Data any `json:"data"`
	}{Data: out}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("eos: %s: %w", path, err)
	}
	return nil
}

// FetchMonth reads, with read-only GETs, what BuildRows needs for one member
// and month: the member's attendances, the month's events and event types.
func (c *Client) FetchMonth(ctx context.Context, bearer string, memberID int64, year int, month time.Month) (*Month, error) {
	m := &Month{Year: year, Month: month}
	if err := c.list(ctx, bearer, "management/event-attendances", url.Values{
		"filters": {encodeFilters(filter{[]string{"member_id"}, "eq", memberID})},
	}, &m.Attendances); err != nil {
		return nil, err
	}
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	if err := c.list(ctx, bearer, "management/events", url.Values{
		"filters": {encodeFilters(
			filter{[]string{"range_from"}, "gte", first.Format(timeLayout)},
			filter{[]string{"range_from"}, "lt", first.AddDate(0, 1, 0).Format(timeLayout)},
		)},
		"with": {"team_ids,related_team_ids"},
	}, &m.Events); err != nil {
		return nil, err
	}
	types, err := c.EventTypes(ctx, bearer)
	if err != nil {
		return nil, err
	}
	m.EventTypes = types
	return m, nil
}

func (c *Client) EventTypes(ctx context.Context, bearer string) ([]EventType, error) {
	var types []EventType
	if err := c.list(ctx, bearer, "event-types", nil, &types); err != nil {
		return nil, err
	}
	return types, nil
}
