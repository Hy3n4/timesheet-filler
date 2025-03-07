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
	FileToken    string
	Names        []string
	Months       []string
	DefaultMonth string
}

type EditTemplateData struct {
	BaseTemplateData
	FileToken string
	Name      string
	Month     string
	TableData []TableRow
}

type DownloadTemplateData struct {
	BaseTemplateData
	DownloadToken string
	FileName      string
	FileToken     string
	Name          string
	Month         string
}

type TableRow struct {
	Date      string
	StartTime string
	EndTime   string
	Note      string
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
