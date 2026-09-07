package handlers

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strconv"

	"timesheet-filler/internal/contextkeys"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
)

type UploadHandler struct {
	excelService    *services.ExcelService
	fileStore       *services.FileStore
	templateService *services.TemplateService
	maxUploadSize   int64
}

func NewUploadHandler(
	excelService *services.ExcelService,
	fileStore *services.FileStore,
	templateService *services.TemplateService,
	maxUploadSize int64,
) *UploadHandler {
	return &UploadHandler{
		excelService:    excelService,
		fileStore:       fileStore,
		templateService: templateService,
		maxUploadSize:   maxUploadSize,
	}
}

func (h *UploadHandler) UploadFormHandler(w http.ResponseWriter, r *http.Request) {
	langValue := r.Context().Value(contextkeys.LanguageKey)
	var lang string
	if langValue != nil {
		lang = langValue.(string)
	} else {
		lang = "en"
	}

	if r.Method != http.MethodGet {
		tmplData := models.BaseTemplateData{
			Error: "Method Not Allowed",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusMethodNotAllowed, lang)
		return
	}

	h.templateService.RenderTemplate(w, "upload.html", nil, http.StatusOK, lang)
}

func (h *UploadHandler) UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	langValue := r.Context().Value(contextkeys.LanguageKey)
	var lang string
	if langValue != nil {
		lang = langValue.(string)
	} else {
		lang = "en"
	}

	if r.Method != http.MethodPost {
		tmplData := models.BaseTemplateData{
			Error: "Method Not Allowed",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusMethodNotAllowed, lang)
		return
	}

	// Limit the size of the uploaded file
	if err := r.ParseMultipartForm(h.maxUploadSize); err != nil {
		tmplData := models.BaseTemplateData{
			Error: "Bad Request: Unable to parse form data.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest, lang)
		log.Printf("Error parsing form data: %v", err)
		return
	}

	// Retrieve the uploaded file
	file, header, err := r.FormFile("excelFile")
	if err != nil {
		tmplData := models.BaseTemplateData{
			Error: "Bad Request: Unable to retrieve file.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest, lang)
		log.Printf("Error retrieving file: %v", err)
		return
	}
	defer file.Close()

	// Log file details
	log.Printf("Received file: %s (%d bytes)", header.Filename, header.Size)

	// Read the file into a buffer
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, file); err != nil {
		tmplData := models.BaseTemplateData{
			Error: "Internal Server Error: Unable to read file.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusInternalServerError, lang)
		log.Printf("Error reading file: %v", err)
		return
	}

	fileData := buf.Bytes()

	// Parse the Excel file to get the list of names and months
	requestedSheet := h.excelService.DefaultSourceSheet()
	names, monthsInt, err := h.excelService.ParseExcelForNamesAndMonths(fileData, requestedSheet)
	if err != nil {
		var availableSheets []string
		if snfErr, ok := services.IsSheetNotFoundError(err); ok {
			availableSheets = snfErr.AvailableSheets
		} else if suErr, ok := services.IsSheetUnusableError(err); ok {
			availableSheets = suErr.AvailableSheets
		}

		if len(availableSheets) > 0 {
			log.Printf("Sheet %q unusable (%v), offering %d alternatives", requestedSheet, err, len(availableSheets))

			fileToken := h.fileStore.StoreFileData(fileData, nil, nil, "")

			tmplData := models.SelectSheetTemplateData{
				BaseTemplateData: models.BaseTemplateData{},
				FileToken:        fileToken,
				RequestedSheet:   requestedSheet,
				AvailableSheets:  availableSheets,
			}
			h.templateService.RenderTemplate(w, "select_sheet.html", tmplData, http.StatusOK, lang)
			return
		}

		log.Printf("Error parsing Excel file: %v", err)
		tmplData := models.BaseTemplateData{
			Error: "Internal Server Error: Unable to parse Excel file: " + err.Error(),
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusInternalServerError, lang)
		return
	}

	var months []string
	for _, m := range monthsInt {
		months = append(months, strconv.Itoa(m))
	}

	// Determine the highest month
	var defaultMonth string
	if len(monthsInt) > 0 {
		maxMonth := monthsInt[len(monthsInt)-1]
		defaultMonth = strconv.Itoa(maxMonth)
	}

	// Store the fileData along with names and months using a unique token
	fileToken := h.fileStore.StoreFileData(fileData, names, months, requestedSheet)

	// Prepare data for the template
	tmplData := models.SelectTemplateData{
		FileToken:    fileToken,
		Names:        names,
		Months:       months,
		DefaultMonth: defaultMonth,
	}

	// Serve the selection form
	h.templateService.RenderTemplate(w, "select.html", tmplData, http.StatusOK, lang)
}
