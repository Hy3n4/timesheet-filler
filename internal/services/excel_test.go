package services

import (
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"

	"timesheet-filler/internal/models"
	"timesheet-filler/internal/testutil"
)

func TestParseExcelForNamesAndMonths(t *testing.T) {
	// Create a test Excel file
	testFileData := testutil.CreateTestExcelFile(t)

	// Create the service
	excelService := NewExcelService("test_template.xlsx", "docházka realizačního týmu")

	// Test parsing
	names, months, err := excelService.ParseExcelForNamesAndMonths(testFileData, "")

	// Check for errors
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify the names
	expectedNames := []string{"Another User", "Test User"}
	if len(names) != len(expectedNames) {
		t.Errorf("Expected %d names, got %d", len(expectedNames), len(names))
	}

	for i, name := range expectedNames {
		if i < len(names) && names[i] != name {
			t.Errorf("Expected name %q at index %d, got %q", name, i, names[i])
		}
	}

	// Verify the months
	expectedMonths := []int{1} // January
	if len(months) != len(expectedMonths) {
		t.Errorf("Expected %d months, got %d", len(expectedMonths), len(months))
	}

	for i, month := range expectedMonths {
		if i < len(months) && months[i] != month {
			t.Errorf("Expected month %d at index %d, got %d", month, i, months[i])
		}
	}
}

func TestExtractTableData(t *testing.T) {
	// Create a test Excel file
	testFileData := testutil.CreateTestExcelFile(t)

	// Create the service
	excelService := NewExcelService("test_template.xlsx", "docházka realizačního týmu")

	// Test extraction for a specific user and month
	tableData, err := excelService.ExtractTableData(testFileData, "Test User", 2023, 1, "")

	// Check for errors
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify the extracted data
	if len(tableData) != 2 {
		t.Errorf("Expected 2 rows, got %d", len(tableData))
	}

	// Check first row
	if len(tableData) > 0 {
		row := tableData[0]
		if row.Date != "2023-01-15" {
			t.Errorf("Expected date '2023-01-15', got %q", row.Date)
		}
		if row.StartTime != "18:00" {
			t.Errorf("Expected start time '18:00', got %q", row.StartTime)
		}
		if row.EndTime != "20:00" {
			t.Errorf("Expected end time '20:00', got %q", row.EndTime)
		}
		if row.Note != "Team Practice" {
			t.Errorf("Expected note 'Team Practice', got %q", row.Note)
		}
	}

	// Check non-existent user
	noData, err := excelService.ExtractTableData(testFileData, "Non Existent", 2023, 1, "")
	if err != nil {
		t.Fatalf("Expected no error for non-existent user, got %v", err)
	}
	if len(noData) != 0 {
		t.Errorf("Expected 0 rows for non-existent user, got %d", len(noData))
	}

	otherYear, err := excelService.ExtractTableData(testFileData, "Test User", 2024, 1, "")
	if err != nil {
		t.Fatalf("Expected no error for another year, got %v", err)
	}
	if len(otherYear) != 0 {
		t.Errorf("Expected 0 rows for another year, got %d", len(otherYear))
	}
}

func possibleEventsFile(t *testing.T, withPossibleColumn bool) []byte {
	t.Helper()
	const sheet = "docházka správců týmu"
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		t.Fatal(err)
	}
	header := []any{"ID", "Člen", nil, nil, nil, nil, "Účast", nil, "Typ události", "Název události", nil, "Začátek", "Konec"}
	if withPossibleColumn {
		for len(header) < 24 {
			header = append(header, nil)
		}
		header = append(header, PossibleEventHeader)
	}
	rows := [][]any{
		header,
		{"1", "Coach", nil, nil, nil, nil, "ano", nil, "Trénink", "Training", nil, "2026-10-15 16:00", "2026-10-15 17:30"},
		{"2", "Coach", nil, nil, nil, nil, "ne", nil, "Trénink", "Skipped training", nil, "2026-10-16 16:00", "2026-10-16 17:30"},
		{"3", "Coach", nil, nil, nil, nil, "ne", nil, "Turnaj", "Tournament", nil, "2026-10-17 08:00", "2026-10-17 15:00"},
	}
	if withPossibleColumn {
		row := rows[3]
		for len(row) < 24 {
			row = append(row, nil)
		}
		rows[3] = append(row, "nominace nepotvrzena")
	}
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTableDataPossibleEvents(t *testing.T) {
	service := NewExcelService("test_template.xlsx", "docházka správců týmu")

	rows, err := service.ExtractTableData(possibleEventsFile(t, true), "Coach", 2026, 10, "")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	want := []models.TableRow{
		{Date: "2026-10-15", StartTime: "16:00", EndTime: "17:30", Note: "Training"},
		{Date: "2026-10-17", StartTime: "08:00", EndTime: "15:00", Note: "Tournament", Optional: true, Reason: "nominace nepotvrzena"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("Expected %+v, got %+v", want, rows)
	}
}

func TestExtractTableDataClassicExportHasNoOptionalRows(t *testing.T) {
	service := NewExcelService("test_template.xlsx", "docházka správců týmu")

	rows, err := service.ExtractTableData(possibleEventsFile(t, false), "Coach", 2026, 10, "")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	want := []models.TableRow{
		{Date: "2026-10-15", StartTime: "16:00", EndTime: "17:30", Note: "Training"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("Expected %+v, got %+v", want, rows)
	}
}
