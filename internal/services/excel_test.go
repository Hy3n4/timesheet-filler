package services

import (
	"testing"
	
	"timesheet-filler/internal/testutil"
)

func TestParseExcelForNamesAndMonths(t *testing.T) {
	// Create a test Excel file
	testFileData := testutil.CreateTestExcelFile(t)
	
	// Create the service
	excelService := NewExcelService("test_template.xlsx")
	
	// Test parsing
	names, months, err := excelService.ParseExcelForNamesAndMonths(testFileData)
	
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
	excelService := NewExcelService("test_template.xlsx")
	
	// Test extraction for a specific user and month
	tableData, err := excelService.ExtractTableData(testFileData, "Test User", 1)
	
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
	noData, err := excelService.ExtractTableData(testFileData, "Non Existent", 1)
	if err != nil {
		t.Fatalf("Expected no error for non-existent user, got %v", err)
	}
	if len(noData) != 0 {
		t.Errorf("Expected 0 rows for non-existent user, got %d", len(noData))
	}
}