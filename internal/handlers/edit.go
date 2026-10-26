package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"timesheet-filler/internal/contextkeys"
	"timesheet-filler/internal/metrics"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
	"timesheet-filler/internal/utils"
)

type EditHandler struct {
	excelService    *services.ExcelService
	fileStore       *services.FileStore
	templateService *services.TemplateService
}

func NewEditHandler(
	excelService *services.ExcelService,
	fileStore *services.FileStore,
	templateService *services.TemplateService,
) *EditHandler {
	return &EditHandler{
		excelService:    excelService,
		fileStore:       fileStore,
		templateService: templateService,
	}
}

func (h *EditHandler) EditHandler(w http.ResponseWriter, r *http.Request) {
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

	// Retrieve form values
	name := manualOr(r, "name")
	period := manualOr(r, "period")
	fileToken := r.FormValue("fileToken")

	if name == "" || period == "" || fileToken == "" {
		tmplData := models.BaseTemplateData{
			Error: "All fields are required.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	m := metrics.GetMetrics()
	m.RecordPersonSelection(name)

	// Retrieve the stored file data
	fileDataStruct, ok := h.fileStore.GetFileData(fileToken)
	if !ok {
		tmplData := models.BaseTemplateData{
			Error: "Invalid session. Please re-upload your file.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	year, month, err := utils.ParsePeriod(period)
	if err != nil {
		tmplData := models.SelectTemplateData{
			BaseTemplateData: models.BaseTemplateData{
				Error: "Invalid month selected.",
			},
			FileToken:     fileToken,
			Names:         fileDataStruct.Names,
			Months:        fileDataStruct.Months,
			DefaultPeriod: period,
		}
		h.templateService.RenderTemplate(w, "select.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	// Extract data from the uploaded Excel file
	tableData, err := h.excelService.ExtractTableData(fileDataStruct.Data, name, year, month, fileDataStruct.SheetName)
	if err != nil {
		tmplData := models.SelectTemplateData{
			BaseTemplateData: models.BaseTemplateData{
				Error: fmt.Sprintf("Failed to extract data: %v", err),
			},
			FileToken:     fileToken,
			Names:         fileDataStruct.Names,
			Months:        fileDataStruct.Months,
			DefaultPeriod: period,
		}
		h.templateService.RenderTemplate(w, "select.html", tmplData, http.StatusInternalServerError, lang)
		return
	}

	// If no data was found, initialize with an empty row
	if len(tableData) == 0 {
		tableData = []models.TableRow{
			{
				Date:      "",
				StartTime: "",
				EndTime:   "",
				Note:      "",
			},
		}
	}

	tmplData := models.EditTemplateData{
		FileToken: fileToken,
		Name:      name,
		Month:     strconv.Itoa(month),
		Year:      strconv.Itoa(year),
		TableData: tableData,
	}

	h.templateService.RenderTemplate(w, "edit.html", tmplData, http.StatusOK, lang)
}

// manualChoice is the select option that switches a field to its "<field>_manual" input.
const manualChoice = "__manual__"

func manualOr(r *http.Request, field string) string {
	value := r.FormValue(field)
	if value == manualChoice {
		value = r.FormValue(field + "_manual")
	}
	return strings.TrimSpace(value)
}
