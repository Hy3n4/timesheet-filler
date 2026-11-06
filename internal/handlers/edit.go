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
		h.templateService.RenderTemplate(w, r, "upload.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	name, allowed := coachName(r, name)
	if !allowed {
		tmplData := models.BaseTemplateData{
			Error: h.templateService.GetTranslator().Translate("auth_err_no_eos_name", lang),
		}
		h.templateService.RenderTemplate(w, r, "upload.html", tmplData, http.StatusForbidden, lang)
		return
	}

	m := metrics.GetMetrics()
	m.RecordPersonSelection(name)

	// Retrieve the stored file data
	fileDataStruct, ok := h.fileStore.GetFileData(fileToken, ownerID(r))
	if !ok {
		tmplData := models.BaseTemplateData{
			Error: "Invalid session. Please re-upload your file.",
		}
		h.templateService.RenderTemplate(w, r, "upload.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	year, month, err := utils.ParsePeriod(period)
	if err != nil {
		tmplData := models.SelectTemplateData{
			BaseTemplateData: models.BaseTemplateData{
				Error: "Invalid month selected.",
			},
			FileToken:     fileToken,
			Names:         visibleNames(r, fileDataStruct.Names),
			CustomName:    isAdmin(r),
			Months:        fileDataStruct.Months,
			DefaultPeriod: period,
		}
		h.templateService.RenderTemplate(w, r, "select.html", tmplData, http.StatusBadRequest, lang)
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
			Names:         visibleNames(r, fileDataStruct.Names),
			CustomName:    isAdmin(r),
			Months:        fileDataStruct.Months,
			DefaultPeriod: period,
		}
		h.templateService.RenderTemplate(w, r, "select.html", tmplData, http.StatusInternalServerError, lang)
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

	h.templateService.RenderTemplate(w, r, "edit.html", tmplData, http.StatusOK, lang)
}

// manualChoice is the select option that switches a field to its "<field>_manual" input.
const manualChoice = "__manual__"

func manualOr(r *http.Request, field string) string {
	if r.FormValue(field) == manualChoice {
		return formPeriod(r, field+"_manual")
	}
	return strings.TrimSpace(r.FormValue(field))
}

// formPeriod reads a "period-picker" (<name>_month and <name>_year selects) as
// "YYYY-MM", falling back to a plain <name> field. A bad pick yields a value
// utils.ParsePeriod rejects.
func formPeriod(r *http.Request, name string) string {
	month, year := r.FormValue(name+"_month"), r.FormValue(name+"_year")
	if month == "" && year == "" {
		return strings.TrimSpace(r.FormValue(name))
	}
	m, errM := strconv.Atoi(month)
	y, errY := strconv.Atoi(year)
	if errM != nil || errY != nil || m < 1 || m > 12 || y < 2000 || y > 2100 {
		return "invalid"
	}
	return fmt.Sprintf("%04d-%02d", y, m)
}
