package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port            string
	MetricsPort     string
	TemplateDir     string
	TemplatePath    string
	MaxUploadSize   int64
	FileTokenExpiry time.Duration
	SheetName       string
}

func New() *Config {
	return &Config{
		Port:            getEnv("PORT", "8080"),
		MetricsPort:     getEnv("METRICS_PORT", "9180"),
		TemplateDir:     getEnv("TEMPLATE_DIR", "templates"),
		TemplatePath:    getEnv("TEMPLATE_PATH", "gorily_timesheet_template_2024.xlsx"),
		MaxUploadSize:   getEnvAsInt64("MAX_UPLOAD_SIZE", 16<<20), // 16MB
		FileTokenExpiry: getEnvAsDuration("FILE_TOKEN_EXPIRY", 24*time.Hour),
		SheetName:       getEnv("SHEET_NAME", "docházka správců {CLUB_TYPE}"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
