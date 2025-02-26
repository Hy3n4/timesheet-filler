package handlers

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strconv"

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
	if r.Method != http.MethodGet {
		tmplData := models.BaseTemplateData{
			Error: "Method Not Allowed",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusMethodNotAllowed)
		return
	}

	h.templateService.RenderTemplate(w, "upload.html", nil, http.StatusOK)
}

func (h *UploadHandler) UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		tmplData := models.BaseTemplateData{
			Error: "Method Not Allowed",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusMethodNotAllowed)
		return
	}

	// Limit the size of the uploaded file
	if err := r.ParseMultipartForm(h.maxUploadSize); err != nil {
		tmplData := models.BaseTemplateData{
			Error: "Bad Request: Unable to parse form data.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest)
		log.Printf("Error parsing form data: %v", err)
		return
	}

	// Retrieve the uploaded file
	file, header, err := r.FormFile("excelFile")
	if err != nil {
		tmplData := models.BaseTemplateData{
			Error: "Bad Request: Unable to retrieve file.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest)
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
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusInternalServerError)
		log.Printf("Error reading file: %v", err)
		return
	}

	fileData := buf.Bytes()

	// Parse the Excel file to get the list of names and months
	names, monthsInt, err := h.excelService.ParseExcelForNamesAndMonths(fileData)
	if err != nil {
		tmplData := models.BaseTemplateData{
			Error: "Internal Server Error: Unable to parse Excel file.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusInternalServerError)
		log.Printf("Error parsing Excel file: %v", err)
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
	fileToken := h.fileStore.StoreFileData(fileData, names, months)

	// Prepare data for the template
	tmplData := models.SelectTemplateData{
		FileToken:    fileToken,
		Names:        names,
		Months:       months,
		DefaultMonth: defaultMonth,
	}

	// Serve the selection form
	h.templateService.RenderTemplate(w, "select.html", tmplData, http.StatusOK)
}
