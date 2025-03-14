package services

import (
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"timesheet-filler/internal/i18n"
	"timesheet-filler/internal/models"
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

func (ts *TemplateService) RenderTemplate(w http.ResponseWriter, tmplName string, data interface{}, statusCode int, lang string) error {
	if lang == "" {
		lang = "en"
	}

	funcMap := template.FuncMap{
		"t": func(key string) string {
			return ts.translator.Translate(key, lang)
		},
	}

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

	w.WriteHeader(statusCode)
	return tmpl.ExecuteTemplate(w, "layout", tmplData)
}

func (ts *TemplateService) GetTranslator() *i18n.Translator {
	return ts.translator
}
