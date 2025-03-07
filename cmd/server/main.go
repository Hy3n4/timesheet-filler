package main

import (
	"context"
	_ "embed"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"timesheet-filler/internal/config"
	"timesheet-filler/internal/handlers"
	"timesheet-filler/internal/middleware"
	"timesheet-filler/internal/services"
)

var favicon []byte

func main() {
	// Load configuration
	cfg := config.New()

	// Initialize services
	fileStore := services.NewFileStore(cfg.FileTokenExpiry, 10*time.Minute)
	excelService := services.NewExcelService(cfg.TemplatePath, cfg.SheetName)
	templateService := services.NewTemplateService(cfg.TemplateDir)

	// Initialize middlewares
	metricsMiddleware := middleware.NewMetricsMiddleware()
	loggingMiddleware := middleware.NewLoggingMiddleware()

	// Initialize handlers
	uploadHandler := handlers.NewUploadHandler(excelService, fileStore, templateService, cfg.MaxUploadSize)
	editHandler := handlers.NewEditHandler(excelService, fileStore, templateService)
	processHandler := handlers.NewProcessHandler(excelService, fileStore, templateService)
	downloadHandler := handlers.NewDownloadHandler(fileStore)
	healthHandler := handlers.NewHealthHandler()

	// Set up HTTP router
	mux := http.NewServeMux()

	// Health check and favicon routes
	mux.HandleFunc("/healthz", healthHandler.LivenessHandler)
	mux.HandleFunc("/readyz", healthHandler.ReadinessHandler)

	// Serve static files from the favicon directory
	mux.Handle("/favicon/", http.StripPrefix("/favicon/", http.FileServer(http.Dir(cfg.TemplateDir+"/favicon"))))
	mux.Handle("/favicon.svg", http.FileServer(http.Dir(cfg.TemplateDir+"/favicon")))
	mux.Handle("/favicon.ico", http.FileServer(http.Dir(cfg.TemplateDir+"/favicon")))

	// Application routes with middleware
	mux.Handle("/", applyMiddlewares(
		http.HandlerFunc(uploadHandler.UploadFormHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("uploadFormHandler")))

	mux.Handle("/upload", applyMiddlewares(
		http.HandlerFunc(uploadHandler.UploadFileHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("uploadFileHandler")))

	mux.Handle("/edit", applyMiddlewares(
		http.HandlerFunc(editHandler.EditHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("editHandler")))

	mux.Handle("/process", applyMiddlewares(
		http.HandlerFunc(processHandler.ProcessHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("processHandler")))

	mux.Handle("/download/", applyMiddlewares(
		http.HandlerFunc(downloadHandler.DownloadHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("downloadHandler")))

	// Create servers
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())

	metricsSrv := &http.Server{
		Addr:    ":" + cfg.MetricsPort,
		Handler: metricsMux,
	}

	// Set the application as ready
	healthHandler.SetReady()

	// Start servers
	go func() {
		log.Printf("Starting application server on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Application server error: %v", err)
		}
	}()

	go func() {
		log.Printf("Starting metrics server on %s", metricsSrv.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Metrics server error: %v", err)
		}
	}()

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Print("Shutting down...")
	healthHandler.SetNotReady()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Application server shutdown failed: %v", err)
	}

	if err := metricsSrv.Shutdown(ctx); err != nil {
		log.Fatalf("Metrics server shutdown failed: %v", err)
	}

	log.Println("Server gracefully stopped.")
}

// applyMiddlewares applies a series of middleware handlers to an http.Handler
func applyMiddlewares(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for _, middleware := range middlewares {
		h = middleware(h)
	}
	return h
}
