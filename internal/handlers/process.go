package handlers

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
	"timesheet-filler/internal/utils"
)

type ProcessHandler struct {
	excelService    *services.ExcelService
	fileStore       *services.FileStore
	templateService *services.TemplateService
}

func NewProcessHandler(
	excelService *services.ExcelService,
	fileStore *services.FileStore,
	templateService *services.TemplateService,
) *ProcessHandler {
	return &ProcessHandler{
		excelService:    excelService,
		fileStore:       fileStore,
		templateService: templateService,
	}
}

func (h *ProcessHandler) ProcessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request in process handler", http.StatusBadRequest)
		return
	}

	fileToken := r.FormValue("fileToken")
	name := r.FormValue("name")
	monthStr := r.FormValue("month")

	if fileToken == "" || name == "" || monthStr == "" {
		tmplData := models.BaseTemplateData{
			Error: "Missing required fields in process handler.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusOK)
		return
	}

	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		tmplData := models.EditTemplateData{
			BaseTemplateData: models.BaseTemplateData{
				Error: "Invalid month value.",
			},
		}
		w.WriteHeader(http.StatusBadRequest)
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusNotFound)
		return
	}

	// Retrieve table data from form
	dates := r.Form["date[]"]
	startTimes := r.Form["start_time[]"]
	endTimes := r.Form["end_time[]"]
	notes := r.Form["note[]"]

	if len(dates) == 0 {
		tmplData := models.EditTemplateData{
			BaseTemplateData: models.BaseTemplateData{
				Error: "Please enter at least one row of data.",
			},
			FileToken: fileToken,
			Name:      name,
			Month:     monthStr,
		}
		h.templateService.RenderTemplate(w, "edit.html", tmplData, http.StatusOK)
		return
	}

	// Prepare data for processing
	var tableData []models.TableRow
	for i := range dates {
		tableData = append(tableData, models.TableRow{
			Date:      dates[i],
			StartTime: startTimes[i],
			EndTime:   endTimes[i],
			Note:      notes[i],
		})
	}

	// Process the Excel file
	processedFile, err := h.excelService.ProcessExcelFile(name, tableData)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error processing Excel file: %v", err)
		return
	}

	// Generate filename
	firstname, lastname := utils.SplitName(name)
	cleanFirstname := utils.RemoveDiacritics(firstname)
	cleanLastname := utils.RemoveDiacritics(lastname)
	filename := fmt.Sprintf("Gorily_vykaz-prace_%02d2024_%s_%s.xlsx", month, cleanFirstname, cleanLastname)
	filename = utils.SanitizeFilename(filename)

	// Write the Excel file to a buffer
	buf := new(bytes.Buffer)
	if err := processedFile.Write(buf); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error writing Excel file to buffer: %v", err)
		return
	}

	// Store the file with a new token for download
	downloadToken := h.fileStore.StoreTempFile(buf.Bytes(), filename)

	// Render the download template
	tmplData := models.DownloadTemplateData{
		BaseTemplateData: models.BaseTemplateData{},
		DownloadToken:    downloadToken,
		FileName:         filename,
	}
	h.templateService.RenderTemplate(w, "download.html", tmplData, http.StatusOK)
}
