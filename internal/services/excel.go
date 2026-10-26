package services

import (
	"bytes"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	metrics "timesheet-filler/internal/metrics"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/utils"
)

type SheetNotFoundError struct {
	SheetName       string
	AvailableSheets []string
}

func (e SheetNotFoundError) Error() string {
	return fmt.Sprintf("Sheet '%s' not found in Excel file", e.SheetName)
}

func IsSheetNotFoundError(err error) (SheetNotFoundError, bool) {
	snfErr, ok := err.(SheetNotFoundError)
	return snfErr, ok
}

type SheetUnusableError struct {
	SheetName       string
	AvailableSheets []string
}

func (e SheetUnusableError) Error() string {
	return fmt.Sprintf("Sheet '%s' contains no rows with a readable date", e.SheetName)
}

func IsSheetUnusableError(err error) (SheetUnusableError, bool) {
	suErr, ok := err.(SheetUnusableError)
	return suErr, ok
}

// PossibleEventHeader names the optional trailing column that marks a row as
// a possible event (the cell holds the reason, e.g. "nominace nepotvrzena").
// The classic EOS export has no such column, so its rows are never optional.
const PossibleEventHeader = "Možná událost"

type ExcelService struct {
	templatePath     string
	sourceSheet      string
	targetSheet      string
	idxClen          int
	idxAttended      int
	idxTypUdalosti   int
	idxNazevUdalosti int
	idxDatum1        int
	idxDatum2        int
}

func NewExcelService(templatePath string, sheetName string) *ExcelService {
	return &ExcelService{
		templatePath:     templatePath,
		sourceSheet:      sheetName,
		targetSheet:      "výkaz práce",
		idxClen:          1,
		idxAttended:      6,
		idxTypUdalosti:   8,
		idxNazevUdalosti: 9,
		idxDatum1:        11,
		idxDatum2:        12,
	}
}

func VerifySheetExists(fileData []byte, sheetName string) (bool, []string, error) {
	// Open the Excel file
	srcFile, err := excelize.OpenReader(bytes.NewReader(fileData))
	if err != nil {
		return false, nil, fmt.Errorf("failed to open Excel file: %w", err)
	}
	defer srcFile.Close()

	// Get all available sheets
	sheets := srcFile.GetSheetList()

	// Check if the requested sheet exists
	for _, sheet := range sheets {
		if sheet == sheetName {
			return true, sheets, nil
		}
	}

	return false, sheets, nil
}

// ParseExcelForNamesAndMonths reads sheetName from the uploaded file. An empty
// sheetName falls back to the sheet configured at construction time.
func (es *ExcelService) ParseExcelForNamesAndMonths(fileData []byte, sheetName string) ([]string, []int, error) {
	if sheetName == "" {
		sheetName = es.sourceSheet
	}
	if sheetName == "" {
		return nil, nil, fmt.Errorf("source sheet name is empty")
	}

	srcFile, err := excelize.OpenReader(bytes.NewReader(fileData))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer srcFile.Close()

	// GetSheetIndex reports -1 with a nil error when the sheet is absent.
	if idx, err := srcFile.GetSheetIndex(sheetName); err != nil || idx == -1 {
		return nil, nil, SheetNotFoundError{
			SheetName:       sheetName,
			AvailableSheets: srcFile.GetSheetList(),
		}
	}

	// Get all rows from the source sheet
	rows, err := srcFile.GetRows(sheetName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get rows from sheet %s: %w", sheetName, err)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("sheet %q is empty", sheetName)
	}

	nameSet := make(map[string]struct{})
	monthSet := make(map[int]struct{})

	for _, row := range rows[1:] { // Skip header row
		clenValue := utils.SafeGetCellValue(row, es.idxClen)
		if clenValue != "" {
			nameSet[clenValue] = struct{}{}
		}

		// Extract start date
		startDateStr := utils.SafeGetCellValue(row, es.idxDatum1)
		if startDateStr != "" {
			startDate, err := utils.ParseDate(startDateStr)
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

	if len(months) == 0 {
		return nil, nil, SheetUnusableError{
			SheetName:       sheetName,
			AvailableSheets: srcFile.GetSheetList(),
		}
	}

	// Sort the slices
	sort.Strings(names)
	sort.Ints(months)

	return names, months, nil
}

// ExtractTableData reads sheetName from the uploaded file. An empty sheetName
// falls back to the sheet configured at construction time.
func (es *ExcelService) ExtractTableData(fileData []byte, name string, year, month int, sheetName string) ([]models.TableRow, error) {
	if sheetName == "" {
		sheetName = es.sourceSheet
	}
	if sheetName == "" {
		return nil, fmt.Errorf("source sheet name is empty")
	}

	srcFile, err := excelize.OpenReader(bytes.NewReader(fileData))
	if err != nil {
		return nil, fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer srcFile.Close()

	// GetSheetIndex reports -1 with a nil error when the sheet is absent.
	if idx, err := srcFile.GetSheetIndex(sheetName); err != nil || idx == -1 {
		return nil, SheetNotFoundError{
			SheetName:       sheetName,
			AvailableSheets: srcFile.GetSheetList(),
		}
	}

	rows, err := srcFile.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to get rows from sheet %s: %w", sheetName, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("sheet %q is empty", sheetName)
	}

	idxPossible := -1
	for i, header := range rows[0] {
		if strings.TrimSpace(header) == PossibleEventHeader {
			idxPossible = i
			break
		}
	}

	var tableData []models.TableRow
	for _, row := range rows[1:] { // Skip header row
		member := utils.SafeGetCellValue(row, es.idxClen)
		if member != name {
			continue
		}

		startDateStr := utils.SafeGetCellValue(row, es.idxDatum1)
		startDate, err := utils.ParseDate(startDateStr)
		if err != nil {
			continue
		}
		if startDate.Year() != year || int(startDate.Month()) != month {
			continue
		}

		endDateStr := utils.SafeGetCellValue(row, es.idxDatum2)
		endDate, err := utils.ParseDate(endDateStr)
		if err != nil {
			continue
		}

		// A non-attended row is offered as optional only when it names a
		// possible-event reason; otherwise it is skipped as before.
		reason := ""
		if utils.SafeGetCellValue(row, es.idxAttended) != "ano" {
			if idxPossible >= 0 {
				reason = strings.TrimSpace(utils.SafeGetCellValue(row, idxPossible))
			}
			if reason == "" {
				continue
			}
		}

		dateEntry := startDate.Format("2006-01-02")
		timeStartEntry := startDate.Format("15:04")
		timeEndEntry := endDate.Format("15:04")
		note := utils.SafeGetCellValue(row, es.idxNazevUdalosti)

		tableData = append(tableData, models.TableRow{
			Date:      dateEntry,
			StartTime: timeStartEntry,
			EndTime:   timeEndEntry,
			Note:      note,
			Optional:  reason != "",
			Reason:    reason,
		})
	}

	return tableData, nil
}

// ProcessExcelFile generates an Excel report based on input data
func (es *ExcelService) ProcessExcelFile(filterName string, tableData []models.TableRow) (*excelize.File, error) {
	startTime := time.Now()
	// Load the existing Excel template
	templateFile, err := excelize.OpenFile(es.templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open template file: %w", err)
	}
	// Note: Do not defer closing templateFile here since we'll return it

	// Check if the target sheet exists in the template file
	if _, err := templateFile.GetSheetIndex(es.targetSheet); err != nil {
		return nil, fmt.Errorf("sheet %q does not exist in the template file", es.targetSheet)
	}

	// Split the filterName into firstname and lastname
	firstname, lastname := utils.SplitName(filterName)

	// Fill firstname and lastname into B3 and B4
	if err := templateFile.SetCellValue(es.targetSheet, "B3", firstname); err != nil {
		return nil, fmt.Errorf("failed to set firstname: %w", err)
	}

	if err := templateFile.SetCellValue(es.targetSheet, "B4", lastname); err != nil {
		return nil, fmt.Errorf("failed to set lastname: %w", err)
	}

	// Starting positions
	startRow := 7 // Data starts from row 7
	maxRows := 31 // Limit to 31 entries (rows 7 to 37)

	// Process the tableData and fill dates and times
	for i, row := range tableData {
		if i >= maxRows {
			break // Limit to maxRows entries
		}

		// Parse the date string into time.Time
		date, err := time.Parse("2006-01-02", row.Date)
		if err != nil {
			log.Printf("Error parsing date at tableData index %d: %v", i, err)
			continue // Skip rows with invalid date
		}

		// Set the date into column A
		cellDate := fmt.Sprintf("A%d", startRow+i)
		if err := templateFile.SetCellValue(es.targetSheet, cellDate, date); err != nil {
			return nil, fmt.Errorf("failed to set date at %s: %w", cellDate, err)
		}

		startTime, err := time.Parse("15:04", row.StartTime)
		if err != nil {
			return nil, fmt.Errorf("failed to parse startTime: %w", err)
		}
		endTime, err := time.Parse("15:04", row.EndTime)
		if err != nil {
			return nil, fmt.Errorf("failed to parse endTime: %w", err)
		}
		serialStartTime := utils.TimeToSerial(startTime.Hour(), startTime.Minute(), startTime.Second())
		serialEndTime := utils.TimeToSerial(endTime.Hour(), endTime.Minute(), endTime.Second())

		// Set start time into column B
		cellStartTime := fmt.Sprintf("B%d", startRow+i)
		if err := templateFile.SetCellValue(es.targetSheet, cellStartTime, serialStartTime); err != nil {
			return nil, fmt.Errorf("failed to set start time at %s: %w", cellStartTime, err)
		}

		// Set end time into column C
		cellEndTime := fmt.Sprintf("C%d", startRow+i)
		if err := templateFile.SetCellValue(es.targetSheet, cellEndTime, serialEndTime); err != nil {
			return nil, fmt.Errorf("failed to set end time at %s: %w", cellEndTime, err)
		}

		if err := templateFile.SetCellStyle(es.targetSheet, cellStartTime, cellEndTime, 25); err != nil {
			return nil, fmt.Errorf("failed to set start time style at %s: %w", cellStartTime, err)
		}

		// Set note into column F
		cellNote := fmt.Sprintf("F%d", startRow+i)
		if err := templateFile.SetCellValue(es.targetSheet, cellNote, row.Note); err != nil {
			return nil, fmt.Errorf("failed to set note at %s: %w", cellNote, err)
		}
	}

	m := metrics.GetMetrics()
	m.RecordFileProcessed(metrics.StageProcess, metrics.StatusSuccess)
	m.RecordProcessingDuration(metrics.StageProcess, time.Since(startTime))

	return templateFile, nil
}

func (es *ExcelService) DefaultSourceSheet() string {
	return es.sourceSheet
}
