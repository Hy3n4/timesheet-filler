package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"timesheet-filler/helpers"
	"timesheet-filler/types"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	timeFormat       = "15:04"
	dateFormat       = "02.01.2006"
	dateParseFormat  = "2006-01-02"
	timeParseFormat  = "3:04:00 PM"
)

var (
	fileStore       = make(map[string]types.FileData)
	fileStoreMu     sync.RWMutex
	tempFileStore   = make(map[string]types.TempFileEntry)
	tempFileStoreMu sync.RWMutex
	ready           int32

	//go:embed templates/favicon/favicon.ico
	favicon []byte

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"handler", "method", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"handler", "method"})
)

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func init() {
	prometheus.MustRegister(httpRequestsTotal)
}

func main() {
	atomic.StoreInt32(&ready, 0)
	go func() {
		atomic.StoreInt32(&ready, 1)
	}()

	srvMux := http.NewServeMux()
	srvMux.HandleFunc("/healthz", livenessHandler)
	srvMux.HandleFunc("/readyz", readinessHandler)
	srvMux.HandleFunc("/favicon.ico", faviconHandler)
	srvMux.Handle("/", instrumentHandler("uploadFormHandler", http.HandlerFunc(uploadFormHandler)))
	srvMux.Handle("/upload", instrumentHandler("uploadFileHandler", http.HandlerFunc(uploadFileHandler)))
	srvMux.Handle("/edit", instrumentHandler("editHandler", http.HandlerFunc(editHandler)))
	srvMux.Handle("/process", instrumentHandler("processHandler", http.HandlerFunc(processHandler)))
	srvMux.Handle("/download/", instrumentHandler("downloadHandler", http.HandlerFunc(downloadHandler)))

	metricsSrvMux := http.NewServeMux()
	metricsSrvMux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:    ":8080",
		Handler: srvMux,
	}

	metricsSrv := &http.Server{
		Addr:    ":9180",
		Handler: metricsSrvMux,
	}

	go func() {
		log.Printf("Starting application server on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Application Server error: %v", err)
		}
	}()

	go func() {
		log.Printf("Starting metrics server on %s", metricsSrv.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Metrics Server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Print("Shutting down...")

	atomic.StoreInt32(&ready, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Application Server Shutdown Failed: %v", err)
	}

	if err := metricsSrv.Shutdown(ctx); err != nil {
		log.Fatalf("Metrics Server Shutdown Failed: %v", err)
	}

	log.Println("Server gracefully stopped.")
}

func instrumentHandler(handlerName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &responseRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}
		next.ServeHTTP(rr, r)
		duration := time.Since(start).Seconds()

		httpRequestDuration.With(prometheus.Labels{
			"handler": handlerName,
			"method":  r.Method,
		}).Observe(duration)

		httpRequestsTotal.With(prometheus.Labels{
			"handler": handlerName,
			"method":  r.Method,
			"status":  fmt.Sprintf("%d", rr.statusCode),
		}).Inc()
	})
}

func livenessHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	response := types.HealthCheckResponse{
		Status: "ok",
	}
	json.NewEncoder(w).Encode(response)
}

// readinessHandler returns HTTP 200 if the app is ready to accept requests
func readinessHandler(w http.ResponseWriter, r *http.Request) {
	if atomic.LoadInt32(&ready) == 1 {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		response := types.HealthCheckResponse{
			Status: "ready",
		}
		json.NewEncoder(w).Encode(response)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Header().Set("Content-Type", "application/json")
		response := types.HealthCheckResponse{
			Status: "not ready",
		}
		json.NewEncoder(w).Encode(response)
	}
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/vnd.microsoft.icon")
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(favicon)
	if err != nil {
		log.Printf("Error writing favicon: %v", err)
	}
}

func uploadFormHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		tmplData := types.BaseTemplateData{
			Error: "Method Not Allowed",
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		renderTemplate(w, "upload.html", tmplData, http.StatusMethodNotAllowed)
		return
	}

	renderTemplate(w, "upload.html", nil, http.StatusOK)
}

func renderTemplate(w http.ResponseWriter, tmplName string, data interface{}, statusCode int) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		// Define any custom functions here
	}).ParseFiles(
		"templates/layout.html",
		filepath.Join("templates", tmplName),
	)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error parsing templates: %v", err)
		return
	}

	tmplData := types.TemplateData{
		Data:        data,
		CurrentYear: time.Now().Year(),
	}

	err = tmpl.ExecuteTemplate(w, "layout", tmplData)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error executing template: %v", err)
	}
}

func uploadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		tmplData := types.BaseTemplateData{
			Error: "Method Not Allowed",
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		renderTemplate(w, "upload.html", tmplData, http.StatusMethodNotAllowed)
		return
	}

	// Limit the size of the uploaded file to 16MB
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		tmplData := types.BaseTemplateData{
			Error: "Bad Request: Unable to parse form data.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData, http.StatusOK)
		log.Printf("Error parsing form data: %v", err)
		return
	}

	// Retrieve the uploaded file
	file, header, err := r.FormFile("excelFile")
	if err != nil {
		tmplData := types.BaseTemplateData{
			Error: "Bad Request: Unable to retrieve file.",
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData, http.StatusBadRequest)
		log.Printf("Error retrieving file: %v", err)
		return
	}
	defer file.Close()

	// Log file details
	log.Printf("Received file: %s (%d bytes)", header.Filename, header.Size)

	// Read the file into a buffer
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, file); err != nil {
		tmplData := types.BaseTemplateData{
			Error: "Internal Server Error: Unable to read file.",
		}
		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "upload.html", tmplData, http.StatusOK)
		log.Printf("Error reading file: %v", err)
		return
	}

	fileData := buf.Bytes()

	// Parse the Excel file to get the list of names and months
	names, monthsInt, err := parseExcelForNamesAndMonths(fileData)
	if err != nil {
		tmplData := types.BaseTemplateData{
			Error: "Internal Server Error: Unable to parse Excel file.",
		}
		w.WriteHeader(http.StatusInternalServerError)
		renderTemplate(w, "upload.html", tmplData, http.StatusOK)
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
	fileToken := helpers.GenerateFileToken()
	fileStoreMu.Lock()
	fileStore[fileToken] = types.FileData{
		Data:   fileData,
		Names:  names,
		Months: months,
	}
	fileStoreMu.Unlock()

	// Prepare data for the template
	tmplData := types.SelectTemplateData{
		FileToken:    fileToken,
		Names:        names,
		Months:       months,
		DefaultMonth: defaultMonth,
	}

	// Serve the selection form
	renderTemplate(w, "select.html", tmplData, http.StatusOK)
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
		tmplData := types.BaseTemplateData{
			Error: "All fields are required.",
		}
		renderTemplate(w, "upload.html", tmplData, http.StatusBadRequest)
		return
	}

	// Retrieve the stored file data
	fileStoreMu.RLock()
	fileDataStruct, ok := fileStore[fileToken]
	fileStoreMu.RUnlock()
	if !ok {
		tmplData := types.BaseTemplateData{
			Error: "Invalid session. Please re-upload your file.",
		}
		renderTemplate(w, "upload.html", tmplData, http.StatusBadRequest)
		return
	}

	// Parse the month string to integer
	month, err := helpers.ParseMonth(monthStr)
	if err != nil {
		tmplData := types.SelectTemplateData{
			BaseTemplateData: types.BaseTemplateData{
				Error: "Invalid month selected.",
			},
			FileToken:    fileToken,
			Names:        fileDataStruct.Names,
			Months:       fileDataStruct.Months,
			DefaultMonth: monthStr,
		}
		renderTemplate(w, "select.html", tmplData, http.StatusBadRequest)
		return
	}

	// Extract data from the uploaded Excel file
	tableData, err := extractTableData(fileDataStruct.Data, name, month)
	if err != nil {
		tmplData := types.SelectTemplateData{
			BaseTemplateData: types.BaseTemplateData{
				Error: fmt.Sprintf("Failed to extract data: %v", err),
			},
			FileToken:    fileToken,
			Names:        fileDataStruct.Names,
			Months:       fileDataStruct.Months,
			DefaultMonth: monthStr,
		}
		renderTemplate(w, "select.html", tmplData, http.StatusInternalServerError)
		return
	}

	// If no data was found, initialize with an empty row
	if len(tableData) == 0 {
		tableData = []types.TableRow{
			{
				Date:      "",
				StartTime: "",
				EndTime:   "",
				Note:      "",
			},
		}
	}

	tmplData := types.EditTemplateData{
		FileToken: fileToken,
		Name:      name,
		Month:     monthStr,
		TableData: tableData,
	}

	renderTemplate(w, "edit.html", tmplData, http.StatusOK)
}

func processHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request in process handler", http.StatusBadRequest)
		return
	}

	fileToken := r.FormValue("fileToken")
	name := r.FormValue("name")
	monthStr := r.FormValue("month")

	if fileToken == "" || name == "" || monthStr == "" {
		tmplData := types.BaseTemplateData{
			Error: "Missing required fields in process handler.",
		}
		renderTemplate(w, "upload.html", tmplData, http.StatusOK)
		return
	}

	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		tmplData := types.EditTemplateData{
			BaseTemplateData: types.BaseTemplateData{
				Error: "Invalid month value.",
			},
		}
		w.WriteHeader(http.StatusBadRequest)
		renderTemplate(w, "upload.html", tmplData, http.StatusNotFound)
		return
	}

	// Retrieve table data from form
	dates := r.Form["date[]"]
	startTimes := r.Form["start_time[]"]
	endTimes := r.Form["end_time[]"]
	notes := r.Form["note[]"]

	if len(dates) == 0 {
		tmplData := types.EditTemplateData{
			BaseTemplateData: types.BaseTemplateData{
				Error: "Please enter at least one row of data.",
			},
			FileToken: fileToken,
			Name:      name,
			Month:     monthStr,
		}
		renderTemplate(w, "edit.html", tmplData, http.StatusOK)
		return
	}

	// Prepare data for processing
	var tableData []types.TableRow
	for i := range dates {
		tableData = append(tableData, types.TableRow{
			Date:      dates[i],
			StartTime: startTimes[i],
			EndTime:   endTimes[i],
			Note:      notes[i],
		})
	}

	// Use the processExcelFile function, adjusted to include edits
	processedFile, err := processExcelFile(name, tableData)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error processing Excel file: %v", err)
		return
	}

	// Generate filename
	firstname, lastname := helpers.SplitName(name)
	cleanFirstname := helpers.RemoveDiacritics(firstname)
	cleanLastname := helpers.RemoveDiacritics(lastname)
	filename := fmt.Sprintf("Gorily_vykaz-prace_%02d2024_%s_%s.xlsx", month, cleanFirstname, cleanLastname)
	filename = helpers.SanitizeFilename(filename)

	downloadToken := helpers.GenerateFileToken()

	err = storeGeneratedFile(downloadToken, processedFile, filename)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error storing generated file: %v", err)
		return
	}

	// Redirect to the download page or render the download template
	tmplData := types.DownloadTemplateData{
		BaseTemplateData: types.BaseTemplateData{},
		DownloadToken:    downloadToken,
		FileName:         filename,
	}
	renderTemplate(w, "download.html", tmplData, http.StatusOK)
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the download token from the URL path
	token := strings.TrimPrefix(r.URL.Path, "/download/")

	// Validate the token
	if token == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Retrieve the file data from temporary storage
	tempFileStoreMu.RLock()
	fileEntry, ok := tempFileStore[token]
	tempFileStoreMu.RUnlock()
	if !ok {
		http.Error(w, "File Not Found", http.StatusNotFound)
		return
	}

	// Set headers for file download
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileEntry.Filename))

	// Write the file data to the response
	_, err := w.Write(fileEntry.Data)
	if err != nil {
		log.Printf("Error sending file: %v", err)
	}

	// Clean up the stored file
	tempFileStoreMu.Lock()
	delete(tempFileStore, token)
	tempFileStoreMu.Unlock()
}

func extractTableData(fileData []byte, name string, month int) ([]types.TableRow, error) {
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

	var tableData []types.TableRow
	for _, row := range rows[1:] { // Skip header row
		member := helpers.SafeGetCellValue(row, idxClen)
		if member != name {
			continue
		}

		startDateStr := helpers.SafeGetCellValue(row, idxDatum1)
		startDate, err := helpers.ParseDate(startDateStr)
		if err != nil {
			continue
		}
		if int(startDate.Month()) != month {
			continue
		}

		endDateStr := helpers.SafeGetCellValue(row, idxDatum2)
		endDate, err := helpers.ParseDate(endDateStr)
		if err != nil {
			continue
		}

		attended := helpers.SafeGetCellValue(row, idxAttended)

		if attended != "ano" {
			if int(startDate.Month()) == month {
				continue
			}
		}

		dateEntry := startDate.Format(dateParseFormat)
		timeStartEntry := startDate.Format(timeFormat)
		timeEndEntry := endDate.Format(timeFormat)
		note := helpers.SafeGetCellValue(row, idxNazevUdalosti)

		tableData = append(tableData, types.TableRow{
			Date:      dateEntry,
			StartTime: timeStartEntry,
			EndTime:   timeEndEntry,
			Note:      note,
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
		clenValue := helpers.SafeGetCellValue(row, idxClen)
		if clenValue != "" {
			nameSet[clenValue] = struct{}{}
		}

		// Extract start date
		startDateStr := helpers.SafeGetCellValue(row, idxDatum1)
		if startDateStr != "" {
			startDate, err := helpers.ParseDate(startDateStr)
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

func processExcelFile(filterName string, tableData []types.TableRow) (*excelize.File, error) {
	// Load the existing Excel template
	templateFile, err := excelize.OpenFile(templateFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open template file: %w", err)
	}
	// Note: Do not defer closing templateFile here since we'll return it

	// Check if the target sheet exists in the template file
	if _, err := templateFile.GetSheetIndex(targetSheetName); err != nil {
		return nil, fmt.Errorf("sheet %q does not exist in the template file", targetSheetName)
	}

	// Split the filterName into firstname and lastname
	firstname, lastname := helpers.SplitName(filterName)

	// Fill firstname and lastname into B3 and B4
	if err := templateFile.SetCellValue(targetSheetName, "B3", firstname); err != nil {
		return nil, fmt.Errorf("failed to set firstname: %w", err)
	}

	if err := templateFile.SetCellValue(targetSheetName, "B4", lastname); err != nil {
		return nil, fmt.Errorf("failed to set lastname: %w", err)
	}

	// Starting positions
	startRow := 7 // Data starts from row 7
	maxRows := 31 // Limit to 31 entries (rows 7 to 37)

	// Process the tableData and fill dates and times
	for i, row := range tableData {
		if i >= maxRows {
			//TODO: fire an error and let the user know, that they have reached the row limit
			break // Limit to maxRows entries
		}

		// Parse the date string into time.Time
		date, err := time.Parse(dateParseFormat, row.Date)
		if err != nil {
			log.Printf("Error parsing date at tableData index %d: %v", i, err)
			continue // Skip rows with invalid date
		}

		// Set the date into column A
		cellDate := fmt.Sprintf("A%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellDate, date); err != nil {
			return nil, fmt.Errorf("failed to set date at %s: %w", cellDate, err)
		}

		startTime, err := time.Parse(timeFormat, row.StartTime)
		if err != nil {
			return nil, fmt.Errorf("failed to parse startTime: %w", err)
		}
		endTime, err := time.Parse(timeFormat, row.EndTime)
		if err != nil {
			return nil, fmt.Errorf("failed to parse startTime: %w", err)
		}
		serialStartTime := helpers.TimeToSerial(startTime.Hour(), startTime.Minute(), startTime.Second())
		serialEndTime := helpers.TimeToSerial(endTime.Hour(), endTime.Minute(), endTime.Second())

		// Set start time into column B
		cellStartTime := fmt.Sprintf("B%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellStartTime, serialStartTime); err != nil {
			return nil, fmt.Errorf("failed to set start time at %s: %w", cellStartTime, err)
		}

		// Set end time into column C
		cellEndTime := fmt.Sprintf("C%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellEndTime, serialEndTime); err != nil {
			return nil, fmt.Errorf("failed to set end time at %s: %w", cellEndTime, err)
		}

		if err := templateFile.SetCellStyle(targetSheetName, cellStartTime, cellEndTime, 25); err != nil {
			return nil, fmt.Errorf("failed to set start time style at %s: %w", cellStartTime, err)
		}

		// Set note into column F
		cellNote := fmt.Sprintf("F%d", startRow+i)
		if err := templateFile.SetCellValue(targetSheetName, cellNote, row.Note); err != nil {
			return nil, fmt.Errorf("failed to set note at %s: %w", cellNote, err)
		}
	}

	return templateFile, nil
}

func storeGeneratedFile(token string, file *excelize.File, fileName string) error {
	// Create a buffer to hold the file data
	buf := new(bytes.Buffer)
	if err := file.Write(buf); err != nil {
		return fmt.Errorf("failed to write Excel file to buffer: %w", err)
	}

	// Store the buffer in a temporary map or file system
	tempFileStoreMu.Lock()
	tempFileStore[token] = types.TempFileEntry{
		Data:      buf.Bytes(),
		Filename:  fileName,
		Timestamp: time.Now(),
	}
	tempFileStoreMu.Unlock()

	// TODO: Set an expiration time for the token

	return nil
}
