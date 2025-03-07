package handlers

import (
	"log"
	"net/http"
	"strconv"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/services"
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
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusBadRequest)
		return
	}

	h.excelService.SetSourceSheet(selectedSheet)

	names, monthsInt, err := h.excelService.ParseExcelForNamesAndMonths(fileData.Data)
	if err != nil {
		log.Printf("Error parsing Excel after sheet selection: %v", err)

		tmplData := models.BaseTemplateData{
			Error: "Failed to parse Excel file: " + err.Error(),
		}
		h.templateService.RenderTemplate(w, "upload.html", tmplData, http.StatusInternalServerError)
		return
	}

	var months []string
	for _, m := range monthsInt {
		months = append(months, strconv.Itoa(m))
	}

	var defaultMonth string
	if len(monthsInt) > 0 {
		maxMonth := monthsInt[len(monthsInt)-1]
		defaultMonth = strconv.Itoa(maxMonth)
	}

	fileToken = h.fileStore.StoreFileData(fileData.Data, names, months, selectedSheet)

	tmplData := models.SelectTemplateData{
		FileToken:    fileToken,
		Names:        names,
		Months:       months,
		DefaultMonth: defaultMonth,
	}

	h.templateService.RenderTemplate(w, "select.html", tmplData, http.StatusOK)
}
