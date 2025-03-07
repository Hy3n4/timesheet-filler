package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Translator struct {
	translations map[string]map[string]string
	defaultLang  string
}

func NewTranslator(translationsDir, defaultLang string) (*Translator, error) {
	t := &Translator{
		translations: make(map[string]map[string]string),
		defaultLang:  defaultLang,
	}

	files, err := os.ReadDir(translationsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read translations directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		lang := strings.TrimSuffix(file.Name(), ".json")
		filePath := filepath.Join(translationsDir, file.Name())

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read translation file %s: %w", filePath, err)
		}

		var langTranslations map[string]string
		if err := json.Unmarshal(data, &langTranslations); err != nil {
			return nil, fmt.Errorf("failed to parse translation file %s: %w", file.Name(), err)
		}

		t.translations[lang] = langTranslations
	}

	return t, nil
}

func (t *Translator) Translate(key, lang string) string {
	if translations, ok := t.translations[lang]; ok {
		if value, ok := translations[key]; ok {
			return value
		}
	}

	if translations, ok := t.translations[t.defaultLang]; ok {
		if value, ok := translations[key]; ok {
			return value
		}
	}

	return key
}

func (t *Translator) TranslateMap(keys map[string]string, lang string) map[string]string {
	result := make(map[string]string, len(keys))
	for k, v := range keys {
		result[k] = t.Translate(v, lang)
	}
	return result
}
