package services

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"timesheet-filler/internal/i18n"
)

// Every page must parse together with the layout, the way RenderTemplate
// loads it, so a template mistake fails here rather than on a live page.
func TestAllTemplatesParse(t *testing.T) {
	translator, err := i18n.NewTranslator("../../translations", "en")
	if err != nil {
		t.Fatal(err)
	}
	ts := NewTemplateService("../../templates", translator)
	pages, err := filepath.Glob("../../templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		if filepath.Base(page) == "layout.html" {
			continue
		}
		if _, err := template.New("").Funcs(ts.funcs("en")).ParseFiles("../../templates/layout.html", page); err != nil {
			t.Errorf("%s: %v", filepath.Base(page), err)
		}
	}
	if len(pages) < 10 {
		t.Errorf("found only %d templates", len(pages))
	}
}

// Every {{t "key"}} in a template needs an English and a Czech text;
// otherwise the page shows the raw key.
func TestTemplateKeysAreTranslated(t *testing.T) {
	pages, err := filepath.Glob("../../templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	keyPattern := regexp.MustCompile(`\{\{\s*t\s+"([a-z0-9_]+)"`)
	for _, lang := range []string{"en", "cs"} {
		raw, err := os.ReadFile(filepath.Join("../../translations", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var texts map[string]string
		if err := json.Unmarshal(raw, &texts); err != nil {
			t.Fatal(err)
		}
		for _, page := range pages {
			body, err := os.ReadFile(page)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range keyPattern.FindAllStringSubmatch(string(body), -1) {
				if texts[m[1]] == "" {
					t.Errorf("%s: %q has no %s text", filepath.Base(page), m[1], lang)
				}
			}
		}
	}
}
