package middleware

import (
	"log"
	"net/http"
	"time"
)

type LoggingMiddleware struct{}

func NewLoggingMiddleware() *LoggingMiddleware {
	return &LoggingMiddleware{}
}

func (m *LoggingMiddleware) LogRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response recorder to capture the status code
		rr := &ResponseRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default status code
		}

		// Call the next handler
		next.ServeHTTP(rr, r)

		// Log the request
		duration := time.Since(start)
		log.Printf(
			"[%s] %s %s %d %s",
			r.Method,
			r.RequestURI,
			r.RemoteAddr,
			rr.statusCode,
			duration,
		)
	})
}
