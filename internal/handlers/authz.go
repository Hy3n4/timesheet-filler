package handlers

import (
	"net/http"

	"timesheet-filler/internal/auth"
)

// ownerID is the signed-in user every stored upload and report belongs to.
// Routes using it sit behind auth.RequireUser, so the user is always set.
func ownerID(r *http.Request) int64 {
	return auth.UserFrom(r.Context()).ID
}

// coachName returns the name the user may build a timesheet for: admins any
// requested name, members only the EOS name an admin linked to their account.
// ok is false for a member without a linked name.
func coachName(r *http.Request, requested string) (name string, ok bool) {
	u := auth.UserFrom(r.Context())
	if u.IsAdmin() {
		return requested, true
	}
	return u.EosName, u.EosName != ""
}

// visibleNames limits the coach list on the select page to what coachName allows.
func visibleNames(r *http.Request, names []string) []string {
	u := auth.UserFrom(r.Context())
	if u.IsAdmin() {
		return names
	}
	if u.EosName == "" {
		return nil
	}
	return []string{u.EosName}
}

func isAdmin(r *http.Request) bool {
	return auth.UserFrom(r.Context()).IsAdmin()
}
