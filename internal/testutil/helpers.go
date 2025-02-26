package testutil

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

// CreateTestExcelFile creates a simple Excel file for testing
func CreateTestExcelFile(t *testing.T) []byte {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()

	// Create a sample sheet structure matching our expected format
	sheetName := "docházka realizačního týmu"
	f.NewSheet(sheetName)

	// Set headers
	f.SetCellValue(sheetName, "A1", "ID")
	f.SetCellValue(sheetName, "B1", "Člen") // Column 1 (index)
	f.SetCellValue(sheetName, "G1", "Účast potvrzena") // Column 6 (index)
	f.SetCellValue(sheetName, "I1", "Typ události") // Column 8 (index)
	f.SetCellValue(sheetName, "J1", "Název události") // Column 9 (index)
	f.SetCellValue(sheetName, "L1", "Od") // Column 11 (index)
	f.SetCellValue(sheetName, "M1", "Do") // Column 12 (index)

	// Add test data
	f.SetCellValue(sheetName, "A2", "1")
	f.SetCellValue(sheetName, "B2", "Test User")
	f.SetCellValue(sheetName, "G2", "ano")
	f.SetCellValue(sheetName, "I2", "training")
	f.SetCellValue(sheetName, "J2", "Team Practice")
	f.SetCellValue(sheetName, "L2", "2023-01-15 18:00")
	f.SetCellValue(sheetName, "M2", "2023-01-15 20:00")

	f.SetCellValue(sheetName, "A3", "2")
	f.SetCellValue(sheetName, "B3", "Test User")
	f.SetCellValue(sheetName, "G3", "ano")
	f.SetCellValue(sheetName, "I3", "game")
	f.SetCellValue(sheetName, "J3", "Championship")
	f.SetCellValue(sheetName, "L3", "2023-01-22 15:30")
	f.SetCellValue(sheetName, "M3", "2023-01-22 17:30")

	f.SetCellValue(sheetName, "A4", "3")
	f.SetCellValue(sheetName, "B4", "Another User")
	f.SetCellValue(sheetName, "G4", "ano")
	f.SetCellValue(sheetName, "I4", "training")
	f.SetCellValue(sheetName, "J4", "Fitness")
	f.SetCellValue(sheetName, "L4", "2023-01-15 16:00")
	f.SetCellValue(sheetName, "M4", "2023-01-15 17:30")

	// Save to buffer
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("failed to write test Excel file: %v", err)
	}

	return buf.Bytes()
}

// CreateMultipartRequest creates a test request with a multipart form
func CreateMultipartRequest(t *testing.T, url, method, fieldName, fileName string, fileContent []byte) (*http.Request, string) {
	t.Helper()

	// Create a buffer to store the multipart form data
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	// Create a form file
	ff, err := w.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	// Copy the file content
	_, err = io.Copy(ff, bytes.NewReader(fileContent))
	if err != nil {
		t.Fatalf("failed to copy file content: %v", err)
	}

	// Close the writer
	err = w.Close()
	if err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	// Create the request
	req := httptest.NewRequest(method, url, &b)
	req.Header.Set("Content-Type", w.FormDataContentType())

	return req, w.FormDataContentType()
}

// AssertStatus checks if the response status matches the expected status
func AssertStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("status code: got %d, want %d", got, want)
	}
}

// AssertContains checks if the body contains the expected string
func AssertContains(t *testing.T, body string, want string) {
	t.Helper()
	if !bytes.Contains([]byte(body), []byte(want)) {
		t.Errorf("body does not contain %q", want)
	}
}
