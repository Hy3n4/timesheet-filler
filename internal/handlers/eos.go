package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"timesheet-filler/internal/auth"
	"timesheet-filler/internal/eos"
	"timesheet-filler/internal/eoslink"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
	"timesheet-filler/internal/utils"
)

type EOSHandler struct {
	links     *eoslink.Service
	templates *services.TemplateService
	fileStore *services.FileStore
}

func NewEOSHandler(links *eoslink.Service, t *services.TemplateService, fs *services.FileStore) *EOSHandler {
	return &EOSHandler{links: links, templates: t, fileStore: fs}
}

type EOSPage struct {
	models.BaseTemplateData
	Link       *eoslink.Link
	PendingOTP bool
	Info       string
	Email      string
}

func (h *EOSHandler) tr(r *http.Request, key string) string {
	return h.templates.GetTranslator().Translate(key, lang(r))
}

// errText maps EOS and link errors to messages; anything else is logged and
// shown as a generic EOS failure.
func (h *EOSHandler) errText(r *http.Request, err error) string {
	keys := []struct {
		err error
		key string
	}{
		{eos.ErrInvalidCredentials, "eos_err_invalid_credentials"},
		{eos.ErrLocked, "eos_err_locked"},
		{eos.ErrOTPSetupRequired, "eos_err_otp_setup"},
		{eos.ErrForbidden, "eos_err_forbidden"},
		{eoslink.ErrNoPendingLogin, "eos_err_otp_expired"},
		{eoslink.ErrReauthRequired, "eos_err_reauth"},
		{eoslink.ErrTooManyTries, "eos_err_too_many"},
	}
	for _, k := range keys {
		if errors.Is(err, k.err) {
			return h.tr(r, k.key)
		}
	}
	log.Printf("eos: %s %s: %v", r.Method, r.URL.Path, err)
	return h.tr(r, "eos_err_unavailable")
}

func (h *EOSHandler) render(w http.ResponseWriter, r *http.Request, data EOSPage, status int) {
	link, pending, err := h.links.Get(r.Context(), ownerID(r))
	if err != nil {
		http.Error(w, h.tr(r, "auth_err_generic"), http.StatusInternalServerError)
		return
	}
	data.Link, data.PendingOTP = link, pending
	if err := h.templates.RenderTemplate(w, r, "eos.html", data, status, lang(r)); err != nil {
		log.Printf("render eos.html: %v", err)
	}
}

// done names the result shown after a redirect, so reloading the page never
// resubmits an EOS login.
var done = map[string]string{
	"linked":   "eos_linked_now",
	"unlinked": "eos_unlinked",
	"left":     "eos_unlinked_session_left",
}

func (h *EOSHandler) Page(w http.ResponseWriter, r *http.Request) {
	var data EOSPage
	if key, ok := done[r.URL.Query().Get("done")]; ok {
		data.Info = h.tr(r, key)
	}
	h.render(w, r, data, http.StatusOK)
}

func redirectDone(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, "/account/eos?done="+result, http.StatusSeeOther)
}

func (h *EOSHandler) Link(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	needOTP, err := h.links.Link(r.Context(), ownerID(r), email, r.FormValue("password"), auth.ClientIP(r))
	data := EOSPage{Email: email}
	switch {
	case err != nil:
		data.Error = h.errText(r, err)
		h.render(w, r, data, http.StatusUnprocessableEntity)
	case needOTP:
		data.Info = h.tr(r, "eos_otp_needed")
		h.render(w, r, data, http.StatusOK)
	default:
		redirectDone(w, r, "linked")
	}
}

func (h *EOSHandler) OTP(w http.ResponseWriter, r *http.Request) {
	var data EOSPage
	if err := h.links.CompleteOTP(r.Context(), ownerID(r), strings.TrimSpace(r.FormValue("otp")), auth.ClientIP(r)); err != nil {
		data.Error = h.errText(r, err)
		h.render(w, r, data, http.StatusUnprocessableEntity)
		return
	}
	redirectDone(w, r, "linked")
}

func (h *EOSHandler) Unlink(w http.ResponseWriter, r *http.Request) {
	ended, err := h.links.Unlink(r.Context(), ownerID(r), auth.ClientIP(r))
	switch {
	case errors.Is(err, eoslink.ErrNotLinked):
		http.Redirect(w, r, "/account/eos", http.StatusSeeOther)
	case err != nil:
		var data EOSPage
		data.Error = h.errText(r, err)
		h.render(w, r, data, http.StatusInternalServerError)
	case ended:
		redirectDone(w, r, "unlinked")
	default:
		redirectDone(w, r, "left")
	}
}

func (h *EOSHandler) uploadError(w http.ResponseWriter, r *http.Request, msg string, status int) {
	if err := h.templates.RenderTemplate(w, r, "upload.html", models.BaseTemplateData{Error: msg}, status, lang(r)); err != nil {
		log.Printf("render upload.html: %v", err)
	}
}

// Load builds the edit page from the signed-in user's own EOS attendance. The
// timesheet name is the user's coach name, for admins too: the data is theirs.
func (h *EOSHandler) Load(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if user.EosName == "" {
		h.uploadError(w, r, h.tr(r, "auth_err_no_eos_name"), http.StatusForbidden)
		return
	}
	period := strings.TrimSpace(r.FormValue("period"))
	year, month, err := utils.ParsePeriod(period)
	if err != nil {
		h.uploadError(w, r, h.tr(r, "eos_err_period"), http.StatusBadRequest)
		return
	}
	rows, err := h.links.MonthRows(r.Context(), user.ID, year, time.Month(month))
	switch {
	case errors.Is(err, eoslink.ErrNotLinked):
		http.Redirect(w, r, "/account/eos", http.StatusSeeOther)
		return
	case errors.Is(err, eoslink.ErrReauthRequired):
		h.render(w, r, EOSPage{BaseTemplateData: models.BaseTemplateData{Error: h.errText(r, err)}}, http.StatusOK)
		return
	case err != nil:
		h.uploadError(w, r, h.errText(r, err), http.StatusBadGateway)
		return
	}
	if len(rows) == 0 {
		rows = []models.TableRow{{}}
	}
	// The process step expects an upload token; an EOS load has no file.
	token := h.fileStore.StoreFileData(user.ID, nil, []string{user.EosName}, []string{period}, "")
	if err := h.templates.RenderTemplate(w, r, "edit.html", models.EditTemplateData{
		FileToken: token,
		Name:      user.EosName,
		Month:     strconv.Itoa(month),
		Year:      strconv.Itoa(year),
		TableData: rows,
	}, http.StatusOK, lang(r)); err != nil {
		log.Printf("render edit.html: %v", err)
	}
}
