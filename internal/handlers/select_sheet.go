package handlers

import (
	"log"
	"net/http"
	"sort"
	"time"
	"timesheet-filler/internal/contextkeys"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
	"timesheet-filler/internal/utils"
)

type SelectSheethandler struct {
	excelService    *services.ExcelService
	fileStore       *services.FileStore
	templateService *services.TemplateService
}

func NewSelectSheetHandler(excelService *services.ExcelService, fileStore *services.FileStore, templateService *services.TemplateService) *SelectSheethandler {
	return &SelectSheethandler{
		excelService:    excelService,
		fileStore:       fileStore,
		templateService: templateService,
	}
}

func (h *SelectSheethandler) SelectSheetHandler(w http.ResponseWriter, r *http.Request) {
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

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	fileToken := r.FormValue("fileToken")
	selectedSheet := r.FormValue("sheetName")

	log.Printf("User selected sheet: '%s'", selectedSheet)

	fileData, ok := h.fileStore.GetFileData(fileToken)
	if !ok {
		tmplData := models.BaseTemplateData{
			Error: "Invalid session. Please re-upload your file.",
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest, lang)
		return
	}

	names, monthsInt, err := h.excelService.ParseExcelForNamesAndMonths(fileData.Data, selectedSheet)
	if err != nil {
		log.Printf("Error parsing Excel after sheet selection: %v", err)

		tmplData := models.BaseTemplateData{
			Error: "Failed to parse Excel file: " + err.Error(),
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusInternalServerError, lang)
		return
	}

	now := time.Now()
	var periods []string
	for _, m := range monthsInt {
		periods = append(periods, utils.PeriodForMonth(m, now))
	}
	sort.Strings(periods)

	var defaultPeriod string
	if len(periods) > 0 {
		defaultPeriod = periods[len(periods)-1]
	}

	fileToken = h.fileStore.StoreFileData(fileData.Data, names, periods, selectedSheet)

	tmplData := models.SelectTemplateData{
		FileToken:     fileToken,
		Names:         names,
		Months:        periods,
		DefaultPeriod: defaultPeriod,
	}

	h.templateService.RenderTemplate(w, "select.html", tmplData, http.StatusOK, lang)
}
