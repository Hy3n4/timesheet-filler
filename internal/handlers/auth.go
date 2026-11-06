package handlers

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"strings"

	"timesheet-filler/internal/auth"
	"timesheet-filler/internal/contextkeys"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
)

type AuthHandler struct {
	auth      *auth.Service
	templates *services.TemplateService
}

func NewAuthHandler(a *auth.Service, t *services.TemplateService) *AuthHandler {
	return &AuthHandler{auth: a, templates: t}
}

type LoginPage struct {
	models.BaseTemplateData
	GoogleEnabled bool
	Email         string
}

type TwoFactorPage struct {
	models.BaseTemplateData
	EmailEnabled bool
	Info         string
}

type EnrollPage struct {
	models.BaseTemplateData
	QR            template.URL
	Secret        string
	RecoveryCodes []string
}

type InvitePage struct {
	models.BaseTemplateData
	Token         string
	Email         string
	GoogleEnabled bool
	Name          string
}

func lang(r *http.Request) string {
	if l, ok := r.Context().Value(contextkeys.LanguageKey).(string); ok && l != "" {
		return l
	}
	return "en"
}

func (h *AuthHandler) tr(r *http.Request, key string) string {
	return h.templates.GetTranslator().Translate(key, lang(r))
}

// errText shows auth errors to the user and hides anything else.
func (h *AuthHandler) errText(r *http.Request, err error) string {
	if msg := err.Error(); strings.HasPrefix(msg, "auth_err_") {
		return h.tr(r, msg)
	}
	log.Printf("auth: %s %s: %v", r.Method, r.URL.Path, err)
	return h.tr(r, "auth_err_generic")
}

func (h *AuthHandler) render(w http.ResponseWriter, r *http.Request, page string, data any, status int) {
	if err := h.templates.RenderTemplate(w, r, page, data, status, lang(r)); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if auth.UserFrom(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := LoginPage{GoogleEnabled: h.auth.GoogleEnabled()}
	if key := r.URL.Query().Get("error"); strings.HasPrefix(key, "auth_err_") {
		data.Error = h.tr(r, key)
	}
	h.render(w, r, "login.html", data, http.StatusOK)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	stage, err := h.auth.PasswordLogin(w, r, email, r.FormValue("password"))
	if err != nil {
		data := LoginPage{GoogleEnabled: h.auth.GoogleEnabled(), Email: email}
		data.Error = h.errText(r, err)
		h.render(w, r, "login.html", data, http.StatusUnauthorized)
		return
	}
	if stage == auth.StageEnroll {
		http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
}

func pendingAt(r *http.Request, stage auth.Stage) bool {
	sess := auth.SessionFrom(r.Context())
	return sess != nil && sess.Stage == stage
}

func (h *AuthHandler) TwoFactorPage(w http.ResponseWriter, r *http.Request) {
	if !pendingAt(r, auth.StageMFA) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h.render(w, r, "two_factor.html", TwoFactorPage{EmailEnabled: h.auth.EmailEnabled()}, http.StatusOK)
}

func (h *AuthHandler) TwoFactor(w http.ResponseWriter, r *http.Request) {
	if !pendingAt(r, auth.StageMFA) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := h.auth.VerifySecondFactor(w, r, r.FormValue("code")); err != nil {
		data := TwoFactorPage{EmailEnabled: h.auth.EmailEnabled()}
		data.Error = h.errText(r, err)
		h.render(w, r, "two_factor.html", data, http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) EmailCode(w http.ResponseWriter, r *http.Request) {
	if !pendingAt(r, auth.StageMFA) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data := TwoFactorPage{EmailEnabled: h.auth.EmailEnabled()}
	if err := h.auth.SendEmailCode(r, h.tr(r, "email_code_subject"), h.tr(r, "email_code_body")); err != nil {
		data.Error = h.errText(r, err)
	} else {
		data.Info = h.tr(r, "email_code_sent")
	}
	h.render(w, r, "two_factor.html", data, http.StatusOK)
}

func (h *AuthHandler) EnrollPage(w http.ResponseWriter, r *http.Request) {
	h.renderEnroll(w, r, "", http.StatusOK)
}

func (h *AuthHandler) renderEnroll(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	if !pendingAt(r, auth.StageEnroll) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	e, err := h.auth.StartEnrollment(r.Context())
	if err != nil {
		http.Error(w, h.errText(r, err), http.StatusInternalServerError)
		return
	}
	data := EnrollPage{QR: template.URL(e.QR), Secret: e.Secret} //nolint:gosec // data: URI we generated
	data.Error = errMsg
	h.render(w, r, "enroll.html", data, status)
}

func (h *AuthHandler) Enroll(w http.ResponseWriter, r *http.Request) {
	if !pendingAt(r, auth.StageEnroll) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	codes, err := h.auth.ConfirmEnrollment(w, r, r.FormValue("code"))
	if err != nil {
		h.renderEnroll(w, r, h.errText(r, err), http.StatusUnprocessableEntity)
		return
	}
	h.render(w, r, "enroll.html", EnrollPage{RecoveryCodes: codes}, http.StatusOK)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	h.auth.Logout(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *AuthHandler) GoogleStart(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.GoogleStart(w, r, r.URL.Query().Get("invite")); err != nil {
		h.googleFailed(w, r, err)
	}
}

func (h *AuthHandler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.GoogleCallback(w, r); err != nil {
		h.googleFailed(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) googleFailed(w http.ResponseWriter, r *http.Request, err error) {
	data := LoginPage{GoogleEnabled: h.auth.GoogleEnabled()}
	data.Error = h.errText(r, err)
	h.render(w, r, "login.html", data, http.StatusUnauthorized)
}

func (h *AuthHandler) InvitePage(w http.ResponseWriter, r *http.Request) {
	h.renderInvite(w, r, "", "", http.StatusOK)
}

func (h *AuthHandler) renderInvite(w http.ResponseWriter, r *http.Request, name, errMsg string, status int) {
	token := r.PathValue("token")
	inv, err := h.auth.InviteByToken(r.Context(), token)
	if err != nil {
		data := LoginPage{GoogleEnabled: h.auth.GoogleEnabled()}
		data.Error = h.errText(r, err)
		h.render(w, r, "login.html", data, http.StatusNotFound)
		return
	}
	data := InvitePage{Token: token, Email: inv.Email, GoogleEnabled: h.auth.GoogleEnabled(), Name: name}
	data.Error = errMsg
	h.render(w, r, "invite.html", data, status)
}

func (h *AuthHandler) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("display_name")
	password := r.FormValue("password")
	if password != r.FormValue("password_confirm") {
		h.renderInvite(w, r, name, h.tr(r, "auth_err_password_mismatch"), http.StatusUnprocessableEntity)
		return
	}
	err := h.auth.AcceptInvitePassword(w, r, r.PathValue("token"), name, password)
	if errors.Is(err, auth.ErrWeakPassword) {
		h.renderInvite(w, r, name, h.errText(r, err), http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		data := LoginPage{GoogleEnabled: h.auth.GoogleEnabled()}
		data.Error = h.errText(r, err)
		h.render(w, r, "login.html", data, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
}
