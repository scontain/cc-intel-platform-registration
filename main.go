package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/opensovereigncloud/cc-intel-platform-registration/pkg/constants"
	"github.com/opensovereigncloud/cc-intel-platform-registration/pkg/metrics"
	"github.com/opensovereigncloud/cc-intel-platform-registration/pkg/registration"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
)

// Version information
var (
	appName   = "cc-intel-platform-registration"
	version   = "dev"
	buildDate = "unknown"
)

func recoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("panic recovered in HTTP handler", slog.Any("panic", r))
					metrics.IncrementPanicCounts()
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte("Internal Server Error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// GetRegistrationServicePort retrieves the Regustration service port from environment variables, defaulting to 8080.
func GetRegistrationServicePort(logger *slog.Logger) string {
	port := constants.DefaultRegistrationServicePort
	portStr := os.Getenv(constants.RegistrationServicePortEnv)
	if portStr != "" {
		parsedPort, err := strconv.Atoi(portStr)
		if err != nil {
			logger.Error("failed to parse registration service port",
				slog.String("env_var", constants.RegistrationServicePortEnv),
				slog.String("err", err.Error()),
				slog.Int("default_value", constants.DefaultRegistrationServicePort))
			// Continue with default value
		} else {
			port = parsedPort
		}
	} else {
		logger.Info("registration service port not set, using default",
			slog.String("env_var", constants.RegistrationServicePortEnv),
			slog.String("default_value", strconv.Itoa(constants.DefaultRegistrationServicePort)))
	}
	// Prepend ":" to form a valid address for http.Server.
	return ":" + strconv.Itoa(port)
}

// GetRegistrationServiceIntervalTime retrieves the registration service interval from environment variables
func GetRegistrationServiceIntervalTime(logger *slog.Logger) time.Duration {
	intervalStr := os.Getenv(constants.DefaultRegistrationServiceIntervalInMinutesEnv)
	interval := constants.DefaultRegistrationServiceIntervalInMinutes
	if intervalStr != "" {
		parsedInterval, err := strconv.Atoi(intervalStr)
		if err != nil {
			logger.Error("failed to parse registration service interval",
				slog.String("env_var", constants.DefaultRegistrationServiceIntervalInMinutesEnv),
				slog.String("err", err.Error()),
				slog.Int("default_value", constants.DefaultRegistrationServiceIntervalInMinutes))
			// Continue with default value
		} else {
			interval = parsedInterval
		}
	} else {
		logger.Info("Registration service interval not set, using default",
			slog.String("env_var", constants.DefaultRegistrationServiceIntervalInMinutesEnv),
			slog.Int("default_value", constants.DefaultRegistrationServiceIntervalInMinutes))
	}
	return time.Duration(interval) * time.Minute
}

// createLogger creates a new zap.Logger with the specified configuration
func createLogger(level string, encoder string, timeEncoding string) (*slog.Logger, error) {
	// Set defaults if not specified
	if level == "" {
		level = "info"
	}

	// Parse the level
	zapLevel, err := zap.ParseAtomicLevel(level)

	if err != nil {
		// Default to info on error
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	// Configure encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.CallerKey = "" // omit the caller

	// Override time encoder if specified
	switch timeEncoding {
	case "rfc3339":
		encoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	case "rfc3339nano":
		encoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	case "iso8601":
		encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	case "millis":
		encoderConfig.EncodeTime = zapcore.EpochMillisTimeEncoder
	case "nanos":
		encoderConfig.EncodeTime = zapcore.EpochNanosTimeEncoder
	default:
		encoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	}

	cfg := zap.NewProductionConfig()
	cfg.DisableCaller = true
	cfg.EncoderConfig = encoderConfig
	cfg.Level = zapLevel

	if encoder == "json" {
		cfg.Encoding = "json"
	} else {
		cfg.Encoding = "console"
	}

	// Build the logger
	zapLogger, err := cfg.Build()

	if err != nil {
		return nil, err
	}

	logger := zapr.NewLogger(zapLogger)
	return slog.New(logr.ToSlogHandler(logger)), nil
}

// runService starts the registration service and HTTP server
func runService(ctx context.Context, logger *slog.Logger) error {
	// Log application startup information
	logger.Info("Application starting",
		slog.String("app", appName),
		slog.String("version", version),
		slog.String("buildDate", buildDate))

	signalCtx, signalCancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer signalCancel()

	intervalDuration := GetRegistrationServiceIntervalTime(logger)
	registrationService := registration.NewRegistrationService(logger, intervalDuration)

	// Create a context with cancel function for shutdown
	g, gCtx := errgroup.WithContext(signalCtx)

	// Start the registration service
	g.Go(func() error {
		// Add panic recovery with metrics
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic in registration service", slog.Any("panic", r))
				metrics.IncrementPanicCounts()
				logger.Info("Incremented the application Panic count metric")

			}
		}()

		return registrationService.Run(gCtx)
	})

	// Setup HTTP server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Service is healthy")
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Service is ready")
	})

	// Create server with timeout configuration
	server := &http.Server{
		Addr:    GetRegistrationServicePort(logger),
		Handler: recoveryMiddleware(logger)(mux),
	}

	// Start the HTTP server in a goroutine
	g.Go(func() error {
		logger.Info("Starting HTTP server",
			slog.String("address", server.Addr),
			slog.Int("intervalMinutes", int(intervalDuration.Minutes())))

		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", slog.String("err", err.Error()))
			return err
		}
		return nil
	})

	// Add server shutdown handler
	g.Go(func() error {
		<-gCtx.Done()
		logger.Info("Shutting down HTTP server")

		// Create a timeout context for shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("http server shutdown error", slog.String("err", err.Error()))
			return err
		}

		logger.Info("HTTP server shutdown completed")
		return nil
	})

	// Wait for all goroutines to complete
	err := g.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("service error", slog.String("err", err.Error()))
		return err
	}

	logger.Info("Service shutdown complete")
	return nil
}

func main() {
	// Define command line flags using pflag
	logLevel := pflag.String("zap-log-level", "", "Log level (debug, info, warn, error)")
	encoder := pflag.String("zap-encoder", "json", "Log encoder (json, console)")
	timeEncoding := pflag.String("zap-time-encoding", "rfc3339nano", "Time encoding (rfc3339, rfc3339nano, iso8601, millis, nanos)")

	// Add help flag
	help := pflag.BoolP("help", "h", false, "Display help information")

	// Parse flags
	pflag.Parse()

	// Display help if requested
	if *help {
		fmt.Printf("Usage of %s:\n", appName)
		pflag.PrintDefaults()
		os.Exit(0)
	}

	// Setup panic handler
	defer func() {
		if r := recover(); r != nil {
			// Create a basic logger for panic case
			logger, _ := createLogger("error", "json", "rfc3339nano")
			if logger != nil {
				logger.Error("application panic", slog.Any("panic", r))
			}
			os.Exit(1)
		}
	}()

	// Create logger
	logger, err := createLogger(*logLevel, *encoder, *timeEncoding)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}

	// Create context
	ctx := context.Background()

	// Run the service
	err = runService(ctx, logger)

	if err != nil {
		os.Exit(1)
	}

}
