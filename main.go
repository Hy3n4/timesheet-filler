package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
)

// Define constants
const (
	sourceSheetName  = "docházka realizačního týmu"
	targetSheetName  = "výkaz práce"
	templateFilePath = "gorily_timesheet_template_2024.xlsx"
	idxClen          = 1  // Člen column
	idxAttended      = 6  // Attendace was confirmed
	idxTypUdalosti   = 8  // Typ události
	idxNazevUdalosti = 9  // Název události
	idxDatum1        = 11 // Start datetime column
	idxDatum2        = 12 // End datetime column
)

type UploadTemplateData struct {
	Error string
}

type SelectTemplateData struct {
	Error     string
	FileToken string
	Names     []string
	Months    []string
}

type FileData struct {
	Data   []byte
	Names  []string
	Months []string
}

var (
	fileStore   = make(map[string]FileData)
	fileStoreMu sync.RWMutex
)

func main() {
	http.HandleFunc("/", uploadFormHandler)       // GET
	http.HandleFunc("/upload", uploadFileHandler) // POST
	http.HandleFunc("/process", processHandler)   // POST

	log.Println("Server started on http://localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func uploadFormHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		tmplData := UploadTemplateData{
			Error: "Method Not Allowed",
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	renderTemplate(w, "upload.html", nil)
}

func renderTemplate(w http.ResponseWriter, templateName string, data interface{}) {
	tmplPath := filepath.Join("templates", templateName)
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error parsing template %s: %v", templateName, err)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error executing template %s: %v", templateName, err)
	}
}

func uploadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		tmplData := UploadTemplateData{
			Error: "Method Not Allowed",
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	// Limit the size of the uploaded file to 16MB
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		tmplData := UploadTemplateData{
			Error: "Bad Request: Unable to parse form data.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		log.Printf("Error parsing form data: %v", err)
		return
	}

	// Retrieve the uploaded file
	file, header, err := r.FormFile("excelFile")
	if err != nil {
		tmplData := UploadTemplateData{
			Error: "Bad Request: Unable to retrieve file.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		log.Printf("Error retrieving file: %v", err)
		return
	}
	defer file.Close()

	// Log file details
	log.Printf("Received file: %s (%d bytes)", header.Filename, header.Size)

	// Read the file into a buffer
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, file); err != nil {
		tmplData := UploadTemplateData{
			Error: "Internal Server Error: Unable to read file.",
		}
		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "upload.html", tmplData)
		log.Printf("Error reading file: %v", err)
		return
	}

	fileData := buf.Bytes()

	// Parse the Excel file to get the list of names and months
	names, months, err := parseExcelForNamesAndMonths(fileData)
	if err != nil {
		tmplData := UploadTemplateData{
			Error: "Internal Server Error: Unable to parse Excel file.",
		}
		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "upload.html", tmplData)
		log.Printf("Error parsing Excel file: %v", err)
		return
	}

	// Store the fileData along with names and months using a unique token
	fileToken := generateFileToken()
	fileStoreMu.Lock()
	fileStore[fileToken] = FileData{
		Data:   fileData,
		Names:  names,
		Months: months,
	}
	fileStoreMu.Unlock()

	// Prepare data for the template
	tmplData := SelectTemplateData{
		Error:     "", // No error message
		FileToken: fileToken,
		Names:     names,
		Months:    months,
	}

	// Serve the selection form
	renderTemplate(w, "select.html", tmplData)
}

func processHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		tmplData := struct {
			Error string
		}{
			Error: "Method Not Allowed",
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		renderTemplate(w, "select.html", tmplData)
		return
	}

	// Get form values
	name := r.FormValue("name")
	monthStr := r.FormValue("month")
	fileToken := r.FormValue("fileToken")

	if name == "" || monthStr == "" || fileToken == "" {
		tmplData := SelectTemplateData{
			Error:     "All fields are required.",
			FileToken: fileToken,
			Names:     []string{}, // Need to populate this
			Months:    []string{},
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "select.html", tmplData)
		log.Println("Missing form fields")
		return
	}

	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		tmplData := struct {
			Error     string
			FileToken string
			Names     []string
			Months    []string
		}{
			Error:     "Invalid month value.",
			FileToken: fileToken,
			Names:     []string{},
			Months:    []string{},
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "select.html", tmplData)
		log.Println("Invalid month value")
		return
	}

	// Retrieve the file data
	fileStoreMu.RLock()
	fileDataStruct, ok := fileStore[fileToken]
	fileStoreMu.RUnlock()
	if !ok {
		tmplData := UploadTemplateData{
			Error: "Invalid file token. Please re-upload your file.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		log.Println("Invalid file token")
		return
	}

	// Process the Excel file
	processedFile, err := processExcelFile(fileDataStruct.Data, name, month)
	if err != nil {
		tmplData := SelectTemplateData{
			Error:     "An error occurred while processing the Excel file." + err.Error(),
			FileToken: fileToken,
			Names:     fileDataStruct.Names,
			Months:    fileDataStruct.Months,
		}

		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "select.html", tmplData)
		return
	}

	// Optionally, remove the file data from the store
	fileStoreMu.Lock()
	delete(fileStore, fileToken)
	fileStoreMu.Unlock()

	// Set headers for file download
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("processed_%s.xlsx", timestamp)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	// Write the processed file to the response
	if err := processedFile.Write(w); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error sending file: %v", err)
		return
	}
}

func parseExcelForNamesAndMonths(fileData []byte) ([]string, []string, error) {
	srcFile, err := excelize.OpenReader(bytes.NewReader(fileData))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			log.Printf("Error closing source file: %v", err)
		}
	}()

	// Check if the source sheet exists
	if _, err := srcFile.GetSheetIndex(sourceSheetName); err != nil {
		return nil, nil, fmt.Errorf("sheet %q does not exist in the uploaded file", sourceSheetName)
	}

	// Get all rows from the source sheet
	rows, err := srcFile.GetRows(sourceSheetName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get rows from sheet %s: %w", sourceSheetName, err)
	}

	nameSet := make(map[string]struct{})
	monthSet := make(map[int]struct{})

	for _, row := range rows[1:] { // Skip header row
		clenValue := safeGetCellValue(row, idxClen)
		if clenValue != "" {
			nameSet[clenValue] = struct{}{}
		}

		// Extract start date
		startDateStr := safeGetCellValue(row, idxDatum1)
		if startDateStr != "" {
			startDate, err := parseDate(startDateStr)
			if err == nil {
				monthSet[int(startDate.Month())] = struct{}{}
			}
		}
	}

	// Convert sets to slices
	var names []string
	for name := range nameSet {
		names = append(names, name)
	}
	var months []string
	for month := range monthSet {
		months = append(months, strconv.Itoa(month))
	}

	// Sort the slices
	sort.Strings(names)
	sort.Slice(months, func(i, j int) bool {
		mi, _ := strconv.Atoi(months[i])
		mj, _ := strconv.Atoi(months[j])
		return mi < mj
	})

	return names, months, nil
}

func generateFileToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("Error generating random token: %v", err)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func processExcelFile(fileData []byte, filterName string, filterMonth int) (*excelize.File, error) {
	// Open the uploaded Excel file from the byte slice
	srcFile, err := excelize.OpenReader(bytes.NewReader(fileData))
	if err != nil {
		return nil, fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			log.Printf("Error closing source file: %v", err)
		}
	}()

	// Load the existing Excel template
	templateFile, err := excelize.OpenFile(templateFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open template file: %w", err)
	}
	defer func() {
		if err := templateFile.Close(); err != nil {
			log.Printf("Error closing template file: %v", err)
		}
	}()

	// Check if the source sheet exists
	if _, err := srcFile.GetSheetIndex(sourceSheetName); err != nil {
		return nil, fmt.Errorf("sheet %q does not exist in the uploaded file", sourceSheetName)
	}

	// Get all rows from the source sheet
	rows, err := srcFile.GetRows(sourceSheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to get rows from sheet %s: %w", sourceSheetName, err)
	}

	// Check if the target sheet exists in the template file
	if _, err := templateFile.GetSheetIndex(targetSheetName); err != nil {
		return nil, fmt.Errorf("sheet %q does not exist in the template file", targetSheetName)
	}

	// Filter rows for the specified name and month
	var filteredRows [][]string
	var firstname, lastname string

	for _, row := range rows[1:] { // Skip header row
		clenValue := safeGetCellValue(row, idxClen)
		if strings.EqualFold(clenValue, filterName) {
			fmt.Sprintf("Getting results for %s", clenValue)
			// Extract start date and time
			startDateStr := safeGetCellValue(row, idxDatum1)
			startDate, err := parseDate(startDateStr)
			if err != nil {
				log.Printf("Error parsing date at row: %v", err)
				continue // Skip rows with invalid dates
			}

			attended := safeGetCellValue(row, idxAttended)

			if attended == "ano" {
				if int(startDate.Month()) == filterMonth {
					filteredRows = append(filteredRows, row)
					firstname, lastname = splitName(clenValue)
				}
			}
		}
	}

	if len(filteredRows) == 0 {
		return nil, fmt.Errorf("no data found for name: %s and month: %d", filterName, filterMonth)
	}

	// Fill firstname and lastname into B3 and C3
	if err := templateFile.SetCellValue(targetSheetName, "B4", firstname); err != nil {
		return nil, fmt.Errorf("failed to set firstname: %w", err)
	}
	if err := templateFile.SetCellValue(targetSheetName, "B3", lastname); err != nil {
		return nil, fmt.Errorf("failed to set lastname: %w", err)
	}

	// Starting positions
	startRow := 7 // Data starts from row 7
	maxRows := 31 // Limit to 31 entries (rows 7 to 37)

	// Process the filtered data and fill dates and times
	for i, row := range filteredRows {
		if i >= maxRows {
			break // Limit to maxRows entries
		}

		// Extract start date and start time from idxDatum1
		startDateStr := safeGetCellValue(row, idxDatum1)
		startDateTime, err := parseDateTime(startDateStr)
		if err != nil {
			log.Printf("Error parsing datetime at row %d: %v", i+2, err)
			continue // Skip rows with invalid datetime
		}

		// Extract end time from idxDatum2
		endTimeStr := safeGetCellValue(row, idxDatum2)
		endDateTime, err := parseDateTime(endTimeStr)
		if err != nil {
			log.Printf("Error parsing datetime at row %d: %v", i+2, err)
			continue // Skip rows with invalid datetime
		}

		nazevUdalosti := safeGetCellValue(row, idxNazevUdalosti)

		// Set date into column A
		cellDate := fmt.Sprintf("A%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellDate, startDateTime.Format("02.01.2006")); err != nil {
			return nil, fmt.Errorf("failed to set date at %s: %w", cellDate, err)
		}

		// Set start time into column B
		cellStartTime := fmt.Sprintf("B%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellStartTime, startDateTime.Format("15:04")); err != nil {
			return nil, fmt.Errorf("failed to set start time at %s: %w", cellStartTime, err)
		}

		// Set end time into column C
		cellEndTime := fmt.Sprintf("C%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellEndTime, endDateTime.Format("15:04")); err != nil {
			return nil, fmt.Errorf("failed to set end time at %s: %w", cellEndTime, err)
		}

		// Set název události into column E
		cellNazevUdalosti := fmt.Sprintf("F%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellNazevUdalosti, nazevUdalosti); err != nil {
			return nil, fmt.Errorf("failed to set název události at %s: %w", cellNazevUdalosti, err)
		}
	}

	return templateFile, nil
}

func splitName(fullName string) (firstname, lastname string) {
	parts := strings.Fields(fullName)
	if len(parts) == 0 {
		return "", ""
	}
	firstname = parts[0]
	if len(parts) > 1 {
		lastname = strings.Join(parts[1:], " ")
	}
	return firstname, lastname
}

func safeGetCellValue(row []string, index int) string {
	if index < len(row) {
		return row[index]
	}
	return ""
}

func parseDateTime(input string) (time.Time, error) {
	layout := "2006-01-02 15:04"
	return time.Parse(layout, input)
}

func parseDate(input string) (time.Time, error) {
	layout := "2006-01-02 15:04"
	return time.Parse(layout, input)
}
