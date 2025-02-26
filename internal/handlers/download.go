package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"timesheet-filler/internal/services"
)

type DownloadHandler struct {
	fileStore *services.FileStore
}

func NewDownloadHandler(fileStore *services.FileStore) *DownloadHandler {
	return &DownloadHandler{
		fileStore: fileStore,
	}
}

func (h *DownloadHandler) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the download token from the URL path
	token := strings.TrimPrefix(r.URL.Path, "/download/")

	// Validate the token
	if token == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Retrieve the file data from temporary storage
	fileEntry, ok := h.fileStore.GetTempFile(token)
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
	h.fileStore.DeleteTempFile(token)
}
