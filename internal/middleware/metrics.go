package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ResponseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rr *ResponseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

type MetricsMiddleware struct {
	requestCounter         *prometheus.CounterVec
	requestDuration        *prometheus.HistogramVec
	fileProcessingCounter  *prometheus.CounterVec
	fileProcessingErrors   *prometheus.CounterVec
	processingDuration     *prometheus.HistogramVec
	fileSizeHistogram      *prometheus.HistogramVec
	rowCountHistogram      *prometheus.HistogramVec
	personSelectionCounter *prometheus.CounterVec
}

func NewMetricsMiddleware() *MetricsMiddleware {
	requestCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"handler", "method", "status"},
	)

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"handler", "method"},
	)

	fileProcessingCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "file_processing_total",
			Help: "Total number of files processed by stage",
		},
		[]string{"stage", "status"},
	)

	fileProcessingErrors := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "file_processing_errors_total",
			Help: "Total number of errors during file processing by stage",
		},
		[]string{"stage", "error_type"},
	)

	processingDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "file_processing_duration_seconds",
			Help:    "Duration of file processing stages in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 5),
		},
		[]string{"stage"},
	)

	fileSizeHistogram := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "file_size_bytes",
			Help:    "Size of processed files in bytes",
			Buckets: prometheus.ExponentialBuckets(1024, 2, 10), // From 1KB to 1MB
		},
		[]string{"stage"},
	)

	rowCountHistogram := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "file_row_count",
			Help:    "Number of rows in processed files",
			Buckets: prometheus.LinearBuckets(5, 5, 20), // From 5 to 100 rows
		},
		[]string{"stage"},
	)

	personSelectionCounter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "person_selection_count",
			Help: "Number of times each person is selected for editing",
		}, []string{"name"})

	prometheus.MustRegister(requestCounter)
	prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(fileProcessingCounter)
	prometheus.MustRegister(fileProcessingErrors)
	prometheus.MustRegister(processingDuration)
	prometheus.MustRegister(fileSizeHistogram)
	prometheus.MustRegister(rowCountHistogram)
	prometheus.MustRegister(personSelectionCounter)

	return &MetricsMiddleware{
		requestCounter:         requestCounter,
		requestDuration:        requestDuration,
		fileProcessingCounter:  fileProcessingCounter,
		fileProcessingErrors:   fileProcessingErrors,
		processingDuration:     processingDuration,
		fileSizeHistogram:      fileSizeHistogram,
		rowCountHistogram:      rowCountHistogram,
		personSelectionCounter: personSelectionCounter,
	}
}

// Instrument returns a middleware function that instruments the next handler
func (m *MetricsMiddleware) Instrument(handlerName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a response recorder to capture the status code
			rr := &ResponseRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK, // Default status code
			}

			// Call the next handler
			next.ServeHTTP(rr, r)

			// Calculate duration
			duration := time.Since(start).Seconds()

			// Record metrics
			m.requestDuration.With(prometheus.Labels{
				"handler": handlerName,
				"method":  r.Method,
			}).Observe(duration)

			m.requestCounter.With(prometheus.Labels{
				"handler": handlerName,
				"method":  r.Method,
				"status":  strconv.Itoa(rr.statusCode),
			}).Inc()
		})
	}
}

func (m *MetricsMiddleware) RecordFileProcessed(stage string, status string) {
	m.fileProcessingCounter.With(prometheus.Labels{
		"stage":  stage,
		"status": status,
	}).Inc()
}

func (m *MetricsMiddleware) RecordFileError(stage string, status string) {
	m.fileProcessingErrors.With(prometheus.Labels{
		"stage":  stage,
		"status": status,
	}).Inc()
}

func (m *MetricsMiddleware) RecordProcessingDuration(stage string, duration time.Duration) {
	m.processingDuration.With(prometheus.Labels{
		"stage": stage,
	}).Observe(duration.Seconds())
}

func (m *MetricsMiddleware) RecordFileSize(stage string, sizeBytes int64) {
	m.fileSizeHistogram.With(prometheus.Labels{
		"stage": stage,
	}).Observe(float64(sizeBytes))
}

func (m *MetricsMiddleware) RecordRowCount(stage string, count int) {
	m.rowCountHistogram.With(prometheus.Labels{
		"stage": stage,
	}).Observe(float64(count))
}

func (m *MetricsMiddleware) RecordPersonSelection(name string) {
	m.personSelectionCounter.With(prometheus.Labels{
		"name": name,
	}).Inc()
}
