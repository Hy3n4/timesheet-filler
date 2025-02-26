package services

import (
	"html/template"
	"net/http"
	"path/filepath"
	"time"

	"timesheet-filler/internal/models"
)

type TemplateService struct {
	templateDir string
}

func NewTemplateService(templateDir string) *TemplateService {
	return &TemplateService{
		templateDir: templateDir,
	}
}

func (ts *TemplateService) RenderTemplate(w http.ResponseWriter, tmplName string, data interface{}, statusCode int) error {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		// Define any custom functions here
	}).ParseFiles(
		filepath.Join(ts.templateDir, "layout.html"),
		filepath.Join(ts.templateDir, tmplName),
	)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return err
	}

	tmplData := models.TemplateData{
		Data:        data,
		CurrentYear: time.Now().Year(),
	}

	w.WriteHeader(statusCode)
	return tmpl.ExecuteTemplate(w, "layout", tmplData)
}
