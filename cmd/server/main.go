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
	"timesheet-filler/internal/i18n"
	"timesheet-filler/internal/middleware"
	"timesheet-filler/internal/services"
)

var favicon []byte
var emailService *services.EmailService

func main() {
	// Load configuration
	cfg := config.New()

	translator, err := i18n.NewTranslator("translations", "en")
	if err != nil {
		log.Fatalf("failed to initialize translator: %v", err)
	}

	// Initialize services
	fileStore := services.NewFileStore(cfg.FileTokenExpiry, 10*time.Minute)
	excelService := services.NewExcelService(cfg.TemplatePath, cfg.SheetName)
	templateService := services.NewTemplateService(cfg.TemplateDir, translator)

	var emailService *services.EmailService
	if cfg.EmailEnabled {
		var provider services.EmailProvider

		switch cfg.EmailProvider {
		case "sendgrid":
			provider = services.ProviderSendGrid
			log.Printf("Email service initialized with SendGrid API")
		case "ses":
			provider = services.ProviderAWSSES
			log.Printf("Email service initialized with AWS SES (region: %s)", cfg.AWSRegion)
		case "oci":
			provider = services.ProviderOCIEmail
			log.Printf("Email service initialized with OCI Email (profile: %s)", cfg.OCICompartmentID)
		case "mailjet":
			provider = services.ProviderMailJet
			log.Printf("Email service initialized with MailJet API")
		default:
			log.Printf("Unknown email provider: %s, defaulting to SendGrid", cfg.EmailProvider)
			provider = services.ProviderSendGrid
		}

		emailService = services.NewEmailService(
			provider,
			cfg.EmailFromEmail,
			cfg.EmailFromName,
			cfg.EmailDefaultTos,
			cfg.SendGridAPIKey,
			cfg.AWSRegion,
			cfg.AWSAccessKeyID,
			cfg.AWSSecretAccessKey,
			cfg.OCIConfigPath,
			cfg.OCIProfileName,
			cfg.OCICompartmentID,
			cfg.OCIEndpointSuffix,
			cfg.MailJetAPIKey,
			cfg.MailJetSecretKey,
		)
	} else {
		log.Println("Email service is disabled")
		emailService = services.NewEmailService(
			services.ProviderSendGrid, "", "", nil, "", "", "", "", "", "", "", "", "", "",
		)
	}

	// Initialize middlewares
	metricsMiddleware := middleware.NewMetricsMiddleware()
	loggingMiddleware := middleware.NewLoggingMiddleware()
	languageMiddleware := middleware.NewLanguageMiddleware("en", []string{"en", "cs"})

	// Initialize handlers
	uploadHandler := handlers.NewUploadHandler(excelService, fileStore, templateService, cfg.MaxUploadSize)
	selectSheetHandler := handlers.NewSelectSheetHandler(excelService, fileStore, templateService)
	editHandler := handlers.NewEditHandler(excelService, fileStore, templateService)
	processHandler := handlers.NewProcessHandler(excelService, fileStore, templateService, cfg.EmailEnabled)
	downloadHandler := handlers.NewDownloadHandler(fileStore)
	healthHandler := handlers.NewHealthHandler()
	emailhandler := handlers.NewEmailHandler(fileStore, emailService, templateService, cfg.EmailEnabled)

	// Set up HTTP router
	baseMux := http.NewServeMux()

	// Health check and favicon routes
	baseMux.HandleFunc("/healthz", healthHandler.LivenessHandler)
	baseMux.HandleFunc("/readyz", healthHandler.ReadinessHandler)

	// Serve static files from the favicon directory
	baseMux.Handle("/favicon/", http.StripPrefix("/favicon/", http.FileServer(http.Dir(cfg.TemplateDir+"/favicon"))))
	baseMux.Handle("/favicon.svg", http.FileServer(http.Dir(cfg.TemplateDir+"/favicon")))
	baseMux.Handle("/favicon.ico", http.FileServer(http.Dir(cfg.TemplateDir+"/favicon")))

	// Application routes with middleware
	baseMux.Handle("/", applyMiddlewares(
		http.HandlerFunc(uploadHandler.UploadFormHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("uploadFormHandler")))

	baseMux.Handle("/upload", applyMiddlewares(
		http.HandlerFunc(uploadHandler.UploadFileHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("uploadFileHandler")))

	baseMux.Handle("/edit", applyMiddlewares(
		http.HandlerFunc(editHandler.EditHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("editHandler")))

	baseMux.Handle("/process", applyMiddlewares(
		http.HandlerFunc(processHandler.ProcessHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("processHandler")))

	baseMux.Handle("/download/", applyMiddlewares(
		http.HandlerFunc(downloadHandler.DownloadHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("downloadHandler")))

	baseMux.Handle("/select-sheet", applyMiddlewares(
		http.HandlerFunc(selectSheetHandler.SelectSheetHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("selectSheetHandler")))

	baseMux.Handle("/send-email", applyMiddlewares(
		http.HandlerFunc(emailhandler.SendEmailHandler),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("sendEmailHandler")))

	// Apply language middleware to all routes
	rootHandler := languageMiddleware.DetectLanguage(baseMux)

	// Create servers
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: rootHandler,
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
