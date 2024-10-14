package types

import "time"

type BaseTemplateData struct {
	Error string
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
	Data   []byte
	Names  []string
	Months []string
}

// Create data structure
type TemplateData struct {
	Data        interface{}
	CurrentYear int
}

type TempFileEntry struct {
	Data      []byte
	Filename  string
	Timestamp time.Time
}