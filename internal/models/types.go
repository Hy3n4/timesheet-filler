package models

import (
	"net/http"
	"time"
)

type BaseTemplateData struct {
	Error string
}

type SelectSheetTemplateData struct {
	BaseTemplateData
	FileToken       string
	RequestedSheet  string
	AvailableSheets []string
}

type SelectTemplateData struct {
	BaseTemplateData
	FileToken     string
	Names         []string
	Months        []string
	DefaultPeriod string
}

type EditTemplateData struct {
	BaseTemplateData
	FileToken string
	Name      string
	Month     string
	Year      string
	TableData []TableRow
}

type DownloadTemplateData struct {
	BaseTemplateData
	DownloadToken string
	FileName      string
	FileToken     string
	Name          string
	Month         string
	EmailOptions  EmailOptions
	EmailSent     bool
	EmailError    string
	EmailEnabled  bool
}

type TableRow struct {
	Date      string
	StartTime string
	EndTime   string
	Note      string
	// Optional marks a possible event the user has to add explicitly;
	// Reason says why it is only possible.
	Optional bool
	Reason   string
}

type FileData struct {
	Data      []byte
	Names     []string
	Months    []string
	SheetName string
	Timestamp time.Time
}

type TemplateData struct {
	Data        interface{}
	CurrentYear int
	CurrentPage string
	Language    string
}

type TempFileEntry struct {
	Data      []byte
	Filename  string
	Timestamp time.Time
}

type ResponseRecorder struct {
	http.ResponseWriter
	statusCode int
}

type HealthCheckResponse struct {
	Status string `json:"status"`
}

type EmailOptions struct {
	SendToSelf bool
	UserEmail  string
}
