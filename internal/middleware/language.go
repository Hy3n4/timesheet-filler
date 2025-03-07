package middleware

import (
	"context"
	"net/http"
	"strings"

	"timesheet-filler/internal/contextkeys"
)

type LanguageMiddleware struct {
	defaultLang    string
	supportedLangs map[string]bool
}

func NewLanguageMiddleware(defaultLang string, supportedLangs []string) *LanguageMiddleware {
	supported := make(map[string]bool)
	for _, lang := range supportedLangs {
		supported[lang] = true
	}

	return &LanguageMiddleware{
		defaultLang:    defaultLang,
		supportedLangs: supported,
	}
}

func (m *LanguageMiddleware) DetectLanguage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var lang string

		// Check URL query parameter first
		langParam := r.URL.Query().Get("lang")
		if langParam != "" && m.supportedLangs[langParam] {
			lang = langParam

			// Set cookie for future requests
			cookie := &http.Cookie{
				Name:     "preferred_language",
				Value:    lang,
				Path:     "/",
				MaxAge:   86400 * 30, // 30 days
				HttpOnly: true,
			}
			http.SetCookie(w, cookie)
		}

		// If no query parameter, check cookie
		if lang == "" {
			if cookie, err := r.Cookie("preferred_language"); err == nil {
				if m.supportedLangs[cookie.Value] {
					lang = cookie.Value
				}
			}
		}

		// Finally, check Accept-Language header
		if lang == "" {
			acceptLang := r.Header.Get("Accept-Language")
			preferredLangs := parseAcceptLanguage(acceptLang)

			for _, preferredLang := range preferredLangs {
				if m.supportedLangs[preferredLang] {
					lang = preferredLang
					break
				}
			}
		}

		// Fall back to default language
		if lang == "" {
			lang = m.defaultLang
		}

		// Store language in request context
		ctx := context.WithValue(r.Context(), contextkeys.LanguageKey, lang)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Helper function to parse Accept-Language header
func parseAcceptLanguage(header string) []string {
	if header == "" {
		return nil
	}

	var languages []string
	parts := strings.Split(header, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Extract language code
		langQ := strings.Split(part, ";")
		lang := strings.Split(langQ[0], "-")[0] // Get the primary language subtag
		languages = append(languages, lang)
	}

	return languages
}
