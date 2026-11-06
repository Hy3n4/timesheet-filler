package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFormPeriod(t *testing.T) {
	for _, tc := range []struct {
		form url.Values
		want string
	}{
		{url.Values{"period_month": {"3"}, "period_year": {"2026"}}, "2026-03"},
		{url.Values{"period": {"2026-11"}}, "2026-11"},
		{url.Values{"period_month": {"13"}, "period_year": {"2026"}}, "invalid"},
		{url.Values{"period_month": {"x"}, "period_year": {"2026"}}, "invalid"},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := formPeriod(r, "period"); got != tc.want {
			t.Errorf("formPeriod(%v) = %q, want %q", tc.form, got, tc.want)
		}
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{
		"period": {"__manual__"}, "period_manual_month": {"12"}, "period_manual_year": {"2025"},
	}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := manualOr(r, "period"); got != "2025-12" {
		t.Errorf("manualOr period = %q, want 2025-12", got)
	}
}
