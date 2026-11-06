package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"timesheet-filler/internal/auth"
	"timesheet-filler/internal/config"
	"timesheet-filler/internal/db"
	"timesheet-filler/internal/eos"
	"timesheet-filler/internal/eoslink"
	"timesheet-filler/internal/handlers"
	"timesheet-filler/internal/i18n"
	"timesheet-filler/internal/metrics"
	"timesheet-filler/internal/middleware"
	"timesheet-filler/internal/secretbox"
	"timesheet-filler/internal/services"
)

var favicon []byte
var emailService *services.EmailService

func main() {
	healthCheck := flag.Bool("health-check", false, "probe /healthz of a running server and exit")
	inviteAdmin := flag.String("invite-admin", "", "create an admin invitation for this email, print its link and exit")
	flag.Parse()

	// Load configuration
	cfg := config.New()

	if *healthCheck {
		resp, err := http.Get("http://127.0.0.1:" + cfg.Port + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	translator, err := i18n.NewTranslator("translations", "en")
	if err != nil {
		log.Fatalf("failed to initialize translator: %v", err)
	}

	// Initialize middlewares
	metricsMiddleware := middleware.NewMetricsMiddleware()
	loggingMiddleware := middleware.NewLoggingMiddleware()
	languageMiddleware := middleware.NewLanguageMiddleware("en", []string{"en", "cs"})

	metrics.SetMetrics(metricsMiddleware)

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
		case "resend":
			provider = services.ProviderResend
			log.Printf("Email service initialized with Resend API")
		default:
			log.Printf("Unknown email provider: %s, defaulting to SendGrid", cfg.EmailProvider)
			provider = services.ProviderSendGrid
		}

		emailService = services.NewEmailService(
			provider,
			cfg.EmailFromEmail,
			cfg.EmailFromName,
			cfg.Emailrecipients,
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
			cfg.ResendAPIKey,
		)
	} else {
		log.Println("Email service is disabled")
		emailService = services.NewEmailService(
			services.ProviderSendGrid, "", "", nil, "", "", "", "", "", "", "", "", "", "", "",
		)
	}

	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	keys, err := secretbox.ParseKeyring(cfg.SecretKeys)
	if err != nil {
		log.Fatalf("SECRET_KEYS: %v", err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 30*time.Second)
	pool, err := db.Open(startCtx, cfg.DatabaseURL)
	cancelStart()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	authService := auth.NewService(pool, keys, auth.Config{
		BaseURL:            cfg.BaseURL,
		GoogleClientID:     cfg.GoogleClientID,
		GoogleClientSecret: cfg.GoogleClientSecret,
		AdminEmails:        cfg.AdminEmails,
		CookieSecure:       cfg.CookieSecure,
		SessionTTL:         cfg.SessionTTL,
		SessionIdle:        cfg.SessionIdle,
	}, emailService)
	if *inviteAdmin != "" {
		token, err := authService.BootstrapAdminInvite(context.Background(), *inviteAdmin)
		if err != nil {
			log.Fatalf("invite-admin: %v", err)
		}
		fmt.Println(authService.InviteLink(token))
		return
	}
	if !authService.GoogleEnabled() {
		log.Println("Google sign-in is disabled (GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET not set)")
	}
	go func() {
		for range time.Tick(10 * time.Minute) {
			authService.PurgeExpired(context.Background())
		}
	}()

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
		auth.RequireUser(http.HandlerFunc(uploadHandler.UploadFormHandler)),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("uploadFormHandler")))

	baseMux.Handle("/upload", applyMiddlewares(
		auth.RequireUser(http.HandlerFunc(uploadHandler.UploadFileHandler)),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("uploadFileHandler")))

	baseMux.Handle("/edit", applyMiddlewares(
		auth.RequireUser(http.HandlerFunc(editHandler.EditHandler)),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("editHandler")))

	baseMux.Handle("/process", applyMiddlewares(
		auth.RequireUser(http.HandlerFunc(processHandler.ProcessHandler)),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("processHandler")))

	baseMux.Handle("/download/", applyMiddlewares(
		auth.RequireUser(http.HandlerFunc(downloadHandler.DownloadHandler)),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("downloadHandler")))

	baseMux.Handle("/select-sheet", applyMiddlewares(
		auth.RequireUser(http.HandlerFunc(selectSheetHandler.SelectSheetHandler)),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("selectSheetHandler")))

	baseMux.Handle("/send-email", applyMiddlewares(
		auth.RequireUser(http.HandlerFunc(emailhandler.SendEmailHandler)),
		loggingMiddleware.LogRequest,
		metricsMiddleware.Instrument("sendEmailHandler")))

	eosLinks := eoslink.NewService(pool, keys, eos.NewClient(cfg.EOSBaseURL))
	authHandler := handlers.NewAuthHandler(authService, templateService, eosLinks)
	eosHandler := handlers.NewEOSHandler(eosLinks, templateService, fileStore)
	userRoute := func(pattern string, h http.HandlerFunc) {
		baseMux.Handle(pattern, applyMiddlewares(auth.RequireUser(h), loggingMiddleware.LogRequest))
	}
	userRoute("GET /account/eos", eosHandler.Page)
	userRoute("POST /account/eos/link", eosHandler.Link)
	userRoute("POST /account/eos/otp", eosHandler.OTP)
	userRoute("POST /account/eos/unlink", eosHandler.Unlink)
	userRoute("POST /eos/load", eosHandler.Load)
	authRoute := func(pattern string, h http.HandlerFunc) {
		baseMux.Handle(pattern, applyMiddlewares(h, loggingMiddleware.LogRequest))
	}
	authRoute("GET /login", authHandler.LoginPage)
	authRoute("POST /login", authHandler.Login)
	authRoute("GET /login/2fa", authHandler.TwoFactorPage)
	authRoute("POST /login/2fa", authHandler.TwoFactor)
	authRoute("POST /login/2fa/email", authHandler.EmailCode)
	authRoute("GET /account/2fa", authHandler.EnrollPage)
	authRoute("POST /account/2fa", authHandler.Enroll)
	authRoute("POST /logout", authHandler.Logout)
	authRoute("GET /auth/google", authHandler.GoogleStart)
	authRoute("GET /auth/google/callback", authHandler.GoogleCallback)
	authRoute("GET /invite/{token}", authHandler.InvitePage)
	authRoute("POST /invite/{token}", authHandler.AcceptInvite)

	adminRoute := func(pattern string, h http.HandlerFunc) {
		baseMux.Handle(pattern, applyMiddlewares(auth.RequireAdmin(h), loggingMiddleware.LogRequest))
	}
	adminRoute("GET /admin", authHandler.Admin)
	adminRoute("POST /admin/invites", authHandler.CreateInvite)
	adminRoute("POST /admin/invites/{id}/renew", authHandler.RenewInvite)
	adminRoute("POST /admin/invites/{id}/revoke", authHandler.RevokeInvite)
	adminRoute("POST /admin/users/{id}", authHandler.UpdateUser)

	// Cross-origin form posts are rejected for every route; sessions are
	// loaded before the language middleware so pages can show the user.
	crossOrigin := http.NewCrossOriginProtection()
	if err := crossOrigin.AddTrustedOrigin(cfg.BaseURL); err != nil {
		log.Fatalf("BASE_URL: %v", err)
	}
	rootHandler := crossOrigin.Handler(authService.Middleware(languageMiddleware.DetectLanguage(baseMux)))

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
