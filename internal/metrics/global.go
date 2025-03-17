package metrics

import (
	"sync"
	"timesheet-filler/internal/middleware"
)

var (
	instance *middleware.MetricsMiddleware
	once     sync.Once
)

func GetMetrics() *middleware.MetricsMiddleware {
	once.Do(func() {
		instance = middleware.NewMetricsMiddleware()
	})
	return instance
}

func SetMetrics(m *middleware.MetricsMiddleware) {
	once.Do(func() {
		instance = m
	})
}
