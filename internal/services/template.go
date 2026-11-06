package services

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"timesheet-filler/internal/contextkeys"
	"timesheet-filler/internal/i18n"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/utils"
)

type TemplateService struct {
	templateDir string
	translator  *i18n.Translator
}

func NewTemplateService(templateDir string, translator *i18n.Translator) *TemplateService {
	return &TemplateService{
		templateDir: templateDir,
		translator:  translator,
	}
}

func (ts *TemplateService) RenderTemplate(w http.ResponseWriter, r *http.Request, tmplName string, data interface{}, statusCode int, lang string) error {
	if lang == "" {
		lang = "en"
	}

	funcMap := ts.funcs(lang)

	tmpl, err := template.New("").Funcs(funcMap).ParseFiles(
		filepath.Join(ts.templateDir, "layout.html"),
		filepath.Join(ts.templateDir, tmplName),
	)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return err
	}

	currentPage := strings.TrimSuffix(tmplName, ".html")

	tmplData := models.TemplateData{
		Data:        data,
		CurrentYear: time.Now().Year(),
		CurrentPage: currentPage,
		Language:    lang,
	}
	if v, ok := r.Context().Value(contextkeys.ViewerKey).(*models.Viewer); ok {
		tmplData.Viewer = v
	}

	w.WriteHeader(statusCode)
	return tmpl.ExecuteTemplate(w, "layout", tmplData)
}

// funcs are the template functions available to every page.
func (ts *TemplateService) funcs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(key string) string {
			return ts.translator.Translate(key, lang)
		},
		"monthName": func(m int) string {
			return ts.translator.Translate(fmt.Sprintf("month_%d", m), lang)
		},
		// periodLabel renders "2026-10" as "October 2026".
		"periodLabel": func(period string) string {
			y, m, err := utils.ParsePeriod(period)
			if err != nil {
				return period
			}
			return fmt.Sprintf("%s %d", ts.translator.Translate(fmt.Sprintf("month_%d", m), lang), y)
		},
		"monthNumbers": func() []int { return []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} },
		"periodYears": func() []int {
			y := time.Now().Year()
			return []int{y - 1, y, y + 1}
		},
		"defaultYear": func() int {
			y, _ := utils.DefaultPeriod(time.Now())
			return y
		},
		"defaultMonth": func() int {
			_, m := utils.DefaultPeriod(time.Now())
			return int(m)
		},
	}
}

func (ts *TemplateService) GetTranslator() *i18n.Translator {
	return ts.translator
}
