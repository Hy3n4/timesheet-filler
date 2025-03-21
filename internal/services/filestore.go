package services

import (
	"log"
	"sync"
	"time"
	"timesheet-filler/internal/metrics"
	"timesheet-filler/internal/models"
	"timesheet-filler/internal/utils"
)

type FileStore struct {
	fileData        map[string]models.FileData
	tempFileData    map[string]models.TempFileEntry
	fileMutex       sync.RWMutex
	tempFileMutex   sync.RWMutex
	expiryTime      time.Duration
	cleanupInterval time.Duration
}

func NewFileStore(expiryTime time.Duration, cleanupInterval time.Duration) *FileStore {
	if cleanupInterval == 0 {
		cleanupInterval = time.Minute * 10
	}
	fs := &FileStore{
		fileData:        make(map[string]models.FileData),
		tempFileData:    make(map[string]models.TempFileEntry),
		expiryTime:      expiryTime,
		cleanupInterval: cleanupInterval,
	}

	// Start cleanup goroutine
	go fs.cleanupRoutine()

	return fs
}

func (fs *FileStore) StoreFileData(data []byte, names []string, months []string, sheetName string) string {
	startTime := time.Now()
	token := utils.GenerateFileToken()

	fs.fileMutex.Lock()
	fs.fileData[token] = models.FileData{
		Data:      data,
		Names:     names,
		Months:    months,
		SheetName: sheetName,
		Timestamp: time.Now(),
	}
	fs.fileMutex.Unlock()

	m := metrics.GetMetrics()
	m.RecordFileProcessed(metrics.StageStorage, metrics.StatusSuccess)
	m.RecordProcessingDuration(metrics.StageStorage, time.Since(startTime))

	return token
}

func (fs *FileStore) GetFileData(token string) (models.FileData, bool) {
	fs.fileMutex.RLock()
	data, ok := fs.fileData[token]
	fs.fileMutex.RUnlock()

	return data, ok
}

func (fs *FileStore) StoreTempFile(data []byte, filename string) string {
	startTime := time.Now()
	token := utils.GenerateFileToken()

	fs.tempFileMutex.Lock()
	fs.tempFileData[token] = models.TempFileEntry{
		Data:      data,
		Filename:  filename,
		Timestamp: time.Now(),
	}
	fs.tempFileMutex.Unlock()

	m := metrics.GetMetrics()
	m.RecordFileProcessed(metrics.StageStorage, metrics.StatusSuccess)
	m.RecordProcessingDuration(metrics.StageStorage, time.Since(startTime))
	m.RecordFileSize(metrics.StageStorage, int64(len(data)))

	log.Printf("Stored temporary file with token: %s, filename: %s, size: %d", token, filename, len(data))
	return token
}

func (fs *FileStore) GetTempFile(token string) (models.TempFileEntry, bool) {
	fs.tempFileMutex.RLock()
	data, ok := fs.tempFileData[token]
	fs.tempFileMutex.RUnlock()

	if !ok {
		fs.tempFileMutex.Lock()
		tokens := make([]string, 0, len(fs.tempFileData))
		for k := range fs.tempFileData {
			tokens = append(tokens, k)
		}
		fs.tempFileMutex.Unlock()

		log.Printf("Available token: %v", token)
	} else {
		log.Printf("GetTempFile: Found token: %s, filename: %s, size: %d, age: %v", token, data.Filename, len(data.Data), time.Since(data.Timestamp))
	}

	return data, ok
}

func (fs *FileStore) DeleteTempFile(token string) {
	fs.tempFileMutex.Lock()
	delete(fs.tempFileData, token)
	fs.tempFileMutex.Unlock()
}

func (fs *FileStore) cleanupRoutine() {
	ticker := time.NewTicker(fs.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()

		// Clean up file data
		fs.fileMutex.Lock()
		for token, data := range fs.fileData {
			if now.Sub(data.Timestamp) > fs.expiryTime {
				delete(fs.fileData, token)
			}
		}
		fs.fileMutex.Unlock()

		// Clean up temp files
		fs.tempFileMutex.Lock()
		for token, data := range fs.tempFileData {
			if now.Sub(data.Timestamp) > fs.expiryTime {
				delete(fs.tempFileData, token)
			}
		}
		fs.tempFileMutex.Unlock()
	}
}

func (fs *FileStore) CleanupExpired() {
	now := time.Now()

	// Clean up file data
	fs.fileMutex.Lock()
	for token, data := range fs.fileData {
		if now.Sub(data.Timestamp) > fs.expiryTime {
			delete(fs.fileData, token)
		}
	}
	fs.fileMutex.Unlock()

	// Clean up temp files
	fs.tempFileMutex.Lock()
	for token, data := range fs.tempFileData {
		if now.Sub(data.Timestamp) > fs.expiryTime {
			delete(fs.tempFileData, token)
		}
	}
	fs.tempFileMutex.Unlock()
}
