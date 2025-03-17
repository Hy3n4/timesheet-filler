package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port               string
	MetricsPort        string
	TemplateDir        string
	TemplatePath       string
	MaxUploadSize      int64
	FileTokenExpiry    time.Duration
	SheetName          string
	EmailEnabled       bool
	EmailProvider      string
	SendGridAPIKey     string
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	OCIConfigPath      string
	OCIProfileName     string
	OCICompartmentID   string
	OCIEndpointSuffix  string
	MailJetAPIKey      string
	MailJetSecretKey   string
	EmailFromName      string
	EmailFromEmail     string
	Emailrecipients    []string
}

func New() *Config {
	return &Config{
		Port:               getEnv("PORT", "8080"),
		MetricsPort:        getEnv("METRICS_PORT", "9180"),
		TemplateDir:        getEnv("TEMPLATE_DIR", "templates"),
		TemplatePath:       getEnv("TEMPLATE_PATH", "gorily_timesheet_template_2024.xlsx"),
		MaxUploadSize:      getEnvAsInt64("MAX_UPLOAD_SIZE", 16<<20), // 16MB
		FileTokenExpiry:    getEnvAsDuration("FILE_TOKEN_EXPIRY", 24*time.Hour),
		SheetName:          getEnv("SHEET_NAME", "docházka správců týmu"),
		EmailEnabled:       getEnvAsBool("EMAIL_ENABLED", false),
		EmailProvider:      getEnv("EMAIL_PROVIDER", "sendgrid"), // Default to SendGrid
		SendGridAPIKey:     getEnv("SENDGRID_API_KEY", ""),
		AWSRegion:          getEnv("AWS_REGION", "eu-central-1"),
		AWSAccessKeyID:     getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: getEnv("AWS_SECRET_ACCESS_KEY", ""),
		OCIConfigPath:      getEnv("OCI_CONFIG_PATH", ""),
		OCIProfileName:     getEnv("OCI_PROFILE_NAME", "DEFAULT"),
		OCICompartmentID:   getEnv("OCI_COMPARTMENT_ID", ""),
		OCIEndpointSuffix:  getEnv("OCI_ENDPOINT_SUFFIX", "oraclecloud.com"),
		MailJetAPIKey:      getEnv("MAILJET_API_KEY", ""),
		MailJetSecretKey:   getEnv("MAILJET_SECRET_KEY", ""),
		EmailFromName:      getEnv("EMAIL_FROM_NAME", "Timesheet Filler"),
		EmailFromEmail:     getEnv("EMAIL_FROM_EMAIL", "gorily.vykaz@hy3n4.com"),
		Emailrecipients:    getEnvAsStringSlice("EMAIL_RECIPIENTS", []string{"hy3nk4@gmail.com"}),
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

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvAsStringSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		return strings.Split(value, ",")
	}
	return defaultValue
}
