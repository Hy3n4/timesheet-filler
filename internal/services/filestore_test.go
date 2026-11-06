package services

import (
	"testing"
	"time"
)

func TestFileStore(t *testing.T) {
	// Create a file store with a short expiry for testing
	fileStore := NewFileStore(100*time.Millisecond, 50*time.Millisecond)

	// Test storing and retrieving file data
	testData := []byte("test data")
	testNames := []string{"Name1", "Name2"}
	testMonths := []string{"1", "2"}

	// Store the data
	token := fileStore.StoreFileData(1, testData, testNames, testMonths, "")

	// Verify the token is not empty
	if token == "" {
		t.Fatal("Expected non-empty token")
	}

	// Retrieve the data
	data, ok := fileStore.GetFileData(token, 1)

	// Verify the data was retrieved
	if !ok {
		t.Fatal("Failed to retrieve stored file data")
	}

	// Verify the data is correct
	if string(data.Data) != string(testData) {
		t.Errorf("Expected data %q, got %q", string(testData), string(data.Data))
	}

	if len(data.Names) != len(testNames) {
		t.Errorf("Expected %d names, got %d", len(testNames), len(data.Names))
	}

	if len(data.Months) != len(testMonths) {
		t.Errorf("Expected %d months, got %d", len(testMonths), len(data.Months))
	}

	// Test temporary file storage
	filename := "test.xlsx"
	tempToken := fileStore.StoreTempFile(1, testData, filename)

	// Retrieve the temp file
	tempFile, ok := fileStore.GetTempFile(tempToken, 1)

	// Verify the temp file was retrieved
	if !ok {
		t.Fatal("Failed to retrieve stored temp file")
	}

	// Verify the temp file data is correct
	if string(tempFile.Data) != string(testData) {
		t.Errorf("Expected temp file data %q, got %q", string(testData), string(tempFile.Data))
	}

	if tempFile.Filename != filename {
		t.Errorf("Expected temp filename %q, got %q", filename, tempFile.Filename)
	}

	// Test deletion of temp file
	fileStore.DeleteTempFile(tempToken)
	_, ok = fileStore.GetTempFile(tempToken, 1)
	if ok {
		t.Error("Temp file should have been deleted")
	}

	// Test expiration (wait for the data to expire)
	time.Sleep(200 * time.Millisecond)
	fileStore.CleanupExpired()

	// Try to retrieve the expired data
	_, ok = fileStore.GetFileData(token, 1)
	if ok {
		t.Error("Data should have expired")
	}
}

func TestFileStoreIsolatesOwners(t *testing.T) {
	fileStore := NewFileStore(time.Hour, time.Hour)
	token := fileStore.StoreFileData(1, []byte("upload"), nil, nil, "")
	if _, ok := fileStore.GetFileData(token, 2); ok {
		t.Error("another user read the upload")
	}
	report := fileStore.StoreTempFile(1, []byte("report"), "r.xlsx")
	if _, ok := fileStore.GetTempFile(report, 2); ok {
		t.Error("another user read the report")
	}
	if _, ok := fileStore.GetTempFile(report, 1); !ok {
		t.Error("the owner lost the report")
	}
}
