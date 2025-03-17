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
	token := strings.TrimPrefix(r.URL.Path, "/download/")

	if token == "" {
		log.Println("Bad Request: Missing token")
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	log.Printf("Download request for token: %s", token)

	fileEntry, ok := h.fileStore.GetTempFile(token)
	if !ok {
		log.Printf("Download error: File not found for token: %s", token)
		http.Error(w, "File Not Found", http.StatusNotFound)
		return
	}

	log.Printf("Found file for download: %s (size: %d bytes", fileEntry.Filename, len(fileEntry.Data))

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileEntry.Filename))

	_, err := w.Write(fileEntry.Data)
	if err != nil {
		log.Printf("Error sending file: %v", err)
	}

	h.fileStore.DeleteTempFile(token)
}
