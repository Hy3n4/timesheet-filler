package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/unicode/norm"
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
	Error        string
	FileToken    string
	Names        []string
	Months       []string
	DefaultMonth string
}

type EditTemplateData struct {
	Error     string
	FileToken string
	Name      string
	Month     string
	TableData template.JS
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
	http.HandleFunc("/", uploadFormHandler)
	http.HandleFunc("/upload", uploadFileHandler)
	http.HandleFunc("/edit", editHandler)
	http.HandleFunc("/process", processHandler)

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
	names, monthsInt, err := parseExcelForNamesAndMonths(fileData)
	if err != nil {
		tmplData := UploadTemplateData{
			Error: "Internal Server Error: Unable to parse Excel file.",
		}
		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "upload.html", tmplData)
		log.Printf("Error parsing Excel file: %v", err)
		return
	}
	var months []string
	for _, m := range monthsInt {
		months = append(months, strconv.Itoa(m))
	}

	// Determine the highest month
	var defaultMonth string
	if len(monthsInt) > 0 {
		maxMonth := monthsInt[len(monthsInt)-1]
		defaultMonth = strconv.Itoa(maxMonth)
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
		Error:        "", // No error message
		FileToken:    fileToken,
		Names:        names,
		Months:       months,
		DefaultMonth: defaultMonth,
	}

	// Serve the selection form
	renderTemplate(w, "select.html", tmplData)
}

func editHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Retrieve form values
	name := r.FormValue("name")
	monthStr := r.FormValue("month")
	fileToken := r.FormValue("fileToken")

	if name == "" || monthStr == "" || fileToken == "" {
		// Handle error
		tmplData := UploadTemplateData{
			Error: "All fields are required.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		// Handle error
		tmplData := SelectTemplateData{
			Error:     "Invalid month value.",
			FileToken: fileToken,
			Names:     []string{}, // Retrieve from session or fileStore
			Months:    []string{},
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "select.html", tmplData)
		return
	}

	// Retrieve the stored file data
	fileStoreMu.RLock()
	fileDataStruct, ok := fileStore[fileToken]
	fileStoreMu.RUnlock()
	if !ok {
		// Handle error
		tmplData := UploadTemplateData{
			Error: "Invalid session. Please re-upload your file.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	// Extract relevant data for the selected name and month
	tableData, err := extractTableData(fileDataStruct.Data, name, month)
	if err != nil {
		// Handle error
		tmplData := SelectTemplateData{
			Error:     "Failed to extract data.",
			FileToken: fileToken,
			Names:     fileDataStruct.Names,
			Months:    fileDataStruct.Months,
		}
		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "select.html", tmplData)
		return
	}

	// Serialize table data to JSON
	tableDataJSON, err := json.Marshal(tableData)
	if err != nil {
		// Handle error
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	tmplData := EditTemplateData{
		Error:     "",
		FileToken: fileToken,
		Name:      name,
		Month:     strconv.Itoa(month),
		TableData: template.JS(tableDataJSON),
	}

	renderTemplate(w, "edit.html", tmplData)
}

func processHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		tmplData := UploadTemplateData{
			Error: "Method Not Allowed",
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	// Get form values
	name := r.FormValue("name")
	monthStr := r.FormValue("month")
	fileToken := r.FormValue("fileToken")

	if name == "" || monthStr == "" || fileToken == "" {
		tmplData := UploadTemplateData{
			Error: "All fields are required.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		tmplData := UploadTemplateData{
			Error: "Invalid month value.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	// Retrieve the stored file data
	fileStoreMu.RLock()
	fileDataStruct, ok := fileStore[fileToken]
	fileStoreMu.RUnlock()
	if !ok {
		tmplData := UploadTemplateData{
			Error: "Invalid session. Please re-upload your file.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData)
		return
	}

	// Process the Excel file
	processedFile, err := processExcelFile(fileDataStruct.Data, name, month)
	if err != nil {
		tmplData := SelectTemplateData{
			Error:        "An error occurred while processing the Excel file.",
			FileToken:    fileToken,
			Names:        fileDataStruct.Names,
			Months:       fileDataStruct.Months,
			DefaultMonth: strconv.Itoa(month),
		}
		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "select.html", tmplData)
		return
	}

	// Extract firstname and lastname
	firstname, lastname := splitName(name)

	// Remove diacritics
	cleanFirstname := removeDiacritics(firstname)
	cleanLastname := removeDiacritics(lastname)

	// Format the filename
	filename := fmt.Sprintf("Gorily_vykaz-prace_%02d2024_%s_%s.xlsx", month, cleanFirstname, cleanLastname)
	filename = sanitizeFilename(filename)
	// Set headers for file download
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	// Write the processed file to the response
	if err := processedFile.Write(w); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error sending file: %v", err)
		return
	}

	// Optionally, clean up the stored file data
	fileStoreMu.Lock()
	delete(fileStore, fileToken)
	fileStoreMu.Unlock()
}

func extractTableData(fileData []byte, name string, month int) ([]map[string]interface{}, error) {
	srcFile, err := excelize.OpenReader(bytes.NewReader(fileData))
	if err != nil {
		return nil, fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer srcFile.Close()

	// Assume data is in a specific sheet and columns
	sheetName := sourceSheetName
	rows, err := srcFile.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to get rows: %w", err)
	}

	var tableData []map[string]interface{}
	for _, row := range rows[1:] { // Skip header row
		member := safeGetCellValue(row, idxClen)
		if member != name {
			continue
		}

		dateStr := safeGetCellValue(row, idxDatum1)
		date, err := parseDate(dateStr)
		if err != nil {
			continue
		}
		if int(date.Month()) != month {
			continue
		}

		description := safeGetCellValue(row, idxNazevUdalosti)
		timeEntry := safeGetCellValue(row, idxDatum1)

		tableData = append(tableData, map[string]interface{}{
			"date":        dateStr,
			"time":        timeEntry,
			"description": description,
		})
	}

	return tableData, nil
}

func parseExcelForNamesAndMonths(fileData []byte) ([]string, []int, error) {
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

	var months []int
	for month := range monthSet {
		months = append(months, month)
	}

	// Sort the slices
	sort.Strings(names)
	sort.Ints(months)

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

func generateExcelReport(tableData []map[string]interface{}, name string, month int) (*excelize.File, error) {
	// Open the template file
	tmplFile, err := excelize.OpenFile(templateFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open template file: %w", err)
	}

	// Insert data into the template
	sheetName := "Sheet1" // Adjust as per your template

	// Start writing from row 2 if row 1 is header
	rowIndex := 2
	for _, row := range tableData {
		dateStr := fmt.Sprintf("%v", row["date"])
		timeEntry := fmt.Sprintf("%v", row["time"])
		description := fmt.Sprintf("%v", row["description"])

		tmplFile.SetCellValue(sheetName, fmt.Sprintf("A%d", rowIndex), dateStr)
		tmplFile.SetCellValue(sheetName, fmt.Sprintf("B%d", rowIndex), name)
		tmplFile.SetCellValue(sheetName, fmt.Sprintf("C%d", rowIndex), description)
		tmplFile.SetCellValue(sheetName, fmt.Sprintf("D%d", rowIndex), timeEntry)
		rowIndex++
	}

	// Return the modified file
	return tmplFile, nil
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

func removeDiacritics(s string) string {
	t := make([]rune, 0, len(s))
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			// Skip non-spacing marks (diacritics)
			continue
		}
		t = append(t, r)
	}
	return string(t)
}

func sanitizeFilename(filename string) string {
	// Replace any invalid characters with underscores
	invalidChars := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
	filename = invalidChars.ReplaceAllString(filename, "_")
	// Trim spaces and periods at the end
	filename = strings.TrimRight(filename, " .")
	return filename
}
