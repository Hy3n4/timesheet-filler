package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"timesheet-filler/internal/contextkeys"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
)

type EmailHandler struct {
	fileStore       *services.FileStore
	emailService    *services.EmailService
	templateService *services.TemplateService
	emailEnabled    bool
}

func NewEmailHandler(
	fileStore *services.FileStore,
	emailService *services.EmailService,
	templateService *services.TemplateService,
	emailEnabled bool,
) *EmailHandler {
	return &EmailHandler{
		fileStore:       fileStore,
		emailService:    emailService,
		templateService: templateService,
		emailEnabled:    emailEnabled,
	}
}

func (h *EmailHandler) SendEmailHandler(w http.ResponseWriter, r *http.Request) {
	langValue := r.Context().Value(contextkeys.LanguageKey)
	var lang string
	if langValue != nil {
		lang = langValue.(string)
	} else {
		lang = "en"
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.emailEnabled || !h.emailService.IsConfigured() {
		tmplData := models.DownloadTemplateData{
			BaseTemplateData: models.BaseTemplateData{
				Error: "Email service is not properly configured",
			},
		}
		h.templateService.RenderTemplate(w, "download.html", tmplData, http.StatusServiceUnavailable, lang)
		return
	}

	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	fileToken := r.FormValue("fileToken")
	downloadToken := r.FormValue("downloadToken")
	fileName := r.FormValue("fileName")
	name := r.FormValue("name")
	month := r.FormValue("month")
	userEmail := r.FormValue("userEmail")
	sendToSelf := r.FormValue("sendToSelf") == "true"

	// Validate inputs
	if fileToken == "" || downloadToken == "" || fileName == "" {
		tmplData := models.BaseTemplateData{
			Error: "Missing required fields",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	// Validate email if user wants to receive a copy
	if sendToSelf && !isValidEmail(userEmail) {
		tmplData := models.DownloadTemplateData{
			BaseTemplateData: models.BaseTemplateData{
				Error: "Please enter a valid email address",
			},
			DownloadToken: downloadToken,
			FileName:      fileName,
			EmailEnabled:  h.emailEnabled,
			EmailOptions: models.EmailOptions{
				SendToSelf: sendToSelf,
				UserEmail:  userEmail,
			},
		}
		h.templateService.RenderTemplate(w, "download.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	// Get the file data
	fileEntry, ok := h.fileStore.GetTempFile(downloadToken)
	if !ok {
		tmplData := models.BaseTemplateData{
			Error: "File not found. It may have expired.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusNotFound, lang)
		return
	}

	// Prepare email recipients
	recipients := h.emailService.DefaultTos
	var ccList []string

	// Add user to CC if requested
	if sendToSelf && userEmail != "" {
		ccList = append(ccList, userEmail)
	}

	// Prepare email subject and body
	subject := fmt.Sprintf("Timesheet Report: %s - Month %s", name, month)
	body := fmt.Sprintf(
		"Attached is the timesheet report for %s for month %s.\n\nThis email was sent automatically from the Timesheet Filler application.",
		name, month)

	// Prepare attachment
	attachment := &services.EmailAttachment{
		FileName:    fileName,
		ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Data:        fileEntry.Data,
	}

	// Send the email
	err := h.emailService.SendEmailWithAttachment(subject, body, recipients, ccList, attachment)

	// Prepare template data
	tmplData := models.DownloadTemplateData{
		DownloadToken: downloadToken,
		FileName:      fileName,
		EmailEnabled:  h.emailEnabled,
		EmailSent:     err == nil,
	}

	if err != nil {
		log.Printf("Error sending email: %v", err)
		tmplData.BaseTemplateData = models.BaseTemplateData{
			Error: fmt.Sprintf("Failed to send email: %v", err),
		}
		tmplData.EmailError = err.Error()
	}

	// Render the download template with email status
	h.templateService.RenderTemplate(w, "download.html", tmplData, http.StatusOK, lang)
}

// Helper function to validate email
func isValidEmail(email string) bool {
	if email == "" {
		return false
	}

	// Basic validation - contains @ and at least one dot after @
	atIndex := strings.Index(email, "@")
	if atIndex < 1 {
		return false
	}

	domain := email[atIndex+1:]
	if len(domain) < 3 || strings.Index(domain, ".") < 1 {
		return false
	}

	return true
}
