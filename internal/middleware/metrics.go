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
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
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

	prometheus.MustRegister(requestCounter)
	prometheus.MustRegister(requestDuration)

	return &MetricsMiddleware{
		requestCounter:  requestCounter,
		requestDuration: requestDuration,
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
