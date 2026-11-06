package handlers

import (
	"fmt"
	"html"
	"net/http"
	"strconv"

	"timesheet-filler/internal/auth"
	"timesheet-filler/internal/models"
)

type AdminPage struct {
	models.BaseTemplateData
	Users        []*auth.User
	Invites      []auth.Invite
	Audit        []auth.AuditEntry
	SelfID       int64
	Info         string
	InviteLink   string
	EmailEnabled bool
}

func (h *AuthHandler) Admin(w http.ResponseWriter, r *http.Request) {
	h.renderAdmin(w, r, AdminPage{}, http.StatusOK)
}

func (h *AuthHandler) renderAdmin(w http.ResponseWriter, r *http.Request, data AdminPage, status int) {
	ctx := r.Context()
	var err error
	if data.Users, err = h.auth.Users(ctx); err == nil {
		if data.Invites, err = h.auth.PendingInvites(ctx); err == nil {
			data.Audit, err = h.auth.AuditLog(ctx, 50)
		}
	}
	if err != nil {
		http.Error(w, h.errText(r, err), http.StatusInternalServerError)
		return
	}
	data.SelfID = auth.UserFrom(ctx).ID
	data.EmailEnabled = h.auth.EmailEnabled()
	h.render(w, r, "admin.html", data, status)
}

func (h *AuthHandler) adminResult(w http.ResponseWriter, r *http.Request, err error, info string) {
	var data AdminPage
	if err != nil {
		data.Error = h.errText(r, err)
		h.renderAdmin(w, r, data, http.StatusUnprocessableEntity)
		return
	}
	data.Info = info
	h.renderAdmin(w, r, data, http.StatusOK)
}

// deliverInvite emails the link when email is configured; the admin page
// shows it once either way, so it can also be handed over another way.
func (h *AuthHandler) deliverInvite(w http.ResponseWriter, r *http.Request, email, token string) {
	link := h.auth.InviteLink(token)
	data := AdminPage{InviteLink: link, Info: h.tr(r, "admin_invite_created")}
	if h.auth.EmailEnabled() {
		body := fmt.Sprintf(h.tr(r, "invite_email_body"), html.EscapeString(link), html.EscapeString(link))
		if err := h.auth.Mailer().SendEmail(h.tr(r, "invite_email_subject"), body, []string{email}, nil); err != nil {
			data.Error = h.errText(r, err)
		} else {
			data.Info = h.tr(r, "admin_invite_sent")
		}
	}
	h.renderAdmin(w, r, data, http.StatusOK)
}

func (h *AuthHandler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	role, err := auth.ParseRole(r.FormValue("role"))
	if err != nil {
		h.adminResult(w, r, err, "")
		return
	}
	email := r.FormValue("email")
	token, err := h.auth.CreateInvite(r, email, role, r.FormValue("eos_name"))
	if err != nil {
		h.adminResult(w, r, err, "")
		return
	}
	h.deliverInvite(w, r, email, token)
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, auth.ErrNotFound
	}
	return id, nil
}

func (h *AuthHandler) RenewInvite(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.adminResult(w, r, err, "")
		return
	}
	token, inv, err := h.auth.RenewInvite(r, id)
	if err != nil {
		h.adminResult(w, r, err, "")
		return
	}
	h.deliverInvite(w, r, inv.Email, token)
}

func (h *AuthHandler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = h.auth.RevokeInvite(r, id)
	}
	h.adminResult(w, r, err, h.tr(r, "admin_done"))
}

func (h *AuthHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.adminResult(w, r, err, "")
		return
	}
	switch r.FormValue("action") {
	case "update":
		var role auth.Role
		if role, err = auth.ParseRole(r.FormValue("role")); err == nil {
			err = h.auth.UpdateUser(r, id, role, r.FormValue("eos_name"))
		}
	case "disable":
		err = h.auth.SetDisabled(r, id, true)
	case "enable":
		err = h.auth.SetDisabled(r, id, false)
	case "reset_mfa":
		err = h.auth.ResetMFA(r, id)
	case "delete":
		err = h.auth.DeleteUser(r, id)
	default:
		err = auth.ErrNotFound
	}
	h.adminResult(w, r, err, h.tr(r, "admin_done"))
}
