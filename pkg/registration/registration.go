package registration

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/opensovereigncloud/cc-intel-platform-registration/internal/pkg/mp_management"
	"github.com/opensovereigncloud/cc-intel-platform-registration/pkg/constants"
	"github.com/opensovereigncloud/cc-intel-platform-registration/pkg/metrics"
)

// RegistrationChecker is an interface to facilitate tests
type RegistrationChecker interface {
	Check() (metrics.StatusCodeMetric, error)
}

func NewRegistrationChecker(logger *slog.Logger) *DefaultRegistrationChecker {
	return &DefaultRegistrationChecker{
		log: logger,
	}
}

type DefaultRegistrationChecker struct {
	log *slog.Logger
}

func (rc *DefaultRegistrationChecker) Check() (metrics.StatusCodeMetric, error) {
	mp, err := mp_management.NewMPManagement()

	if err != nil {
		log.Fatal(err)
	}
	defer mp.Close()

	isMachineRegistered, err := mp.IsMachineRegistered()
	if err != nil {
		rc.log.Error("unable to get the machine registration status", slog.String("error", err.Error()))
		return metrics.StatusCodeMetric{Status: metrics.SgxUefiUnavailable}, nil
	}

	if !isMachineRegistered {
		plaformManifest, err := mp.GetPlatformManifest()
		if err != nil {
			rc.log.Error("unable to get platform manifests ", slog.String("error", err.Error()))
			return metrics.StatusCodeMetric{Status: metrics.SgxUefiUnavailable}, nil
		}
		metric, err := rc.registerPlatform(plaformManifest)

		// registration was successful
		if metric.Status == metrics.PlatformRebootNeeded {
			err := mp.CompleteMachineRegistrationStatus()
			if err != nil {
				rc.log.Error("unable to set registration status UEFI variable as complete ", slog.String("error", err.Error()))
				return metrics.StatusCodeMetric{Status: metrics.UefiPersistFailed}, nil
			}
		}
		return metric, err

	}

	// todo implement all cases here
	return metrics.StatusCodeMetric{Status: metrics.UnknownError}, nil
}

func (r *DefaultRegistrationChecker) registerPlatform(platformManifest mp_management.PlatformManifest) (metrics.StatusCodeMetric, error) {
	ctx, cancel := context.WithTimeout(context.TODO(), time.Duration(constants.IntelRegirationRequestTimeoutInMinutes*time.Minute))
	defer cancel()
	client := &http.Client{}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, constants.IntelPlatformRegistrationEndpoint, bytes.NewReader(platformManifest))
	if err != nil {
		r.log.Error("failed to create request", slog.String("error", err.Error()))
		return metrics.CreateUnknownErrorStatusCodeMetric(), fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	// Execute request
	resp, err := client.Do(req)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			r.log.Error("request timeout to Intel registration service", slog.String("error", err.Error()))
			return metrics.CreateUnknownErrorStatusCodeMetric(), fmt.Errorf("connection timeout: %w", err)
		}
		r.log.Error("failed to send request to Intel registration service", slog.String("error", err.Error()))
		return metrics.CreateUnknownErrorStatusCodeMetric(), fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 201 {
		return metrics.StatusCodeMetric{Status: metrics.PlatformRebootNeeded}, nil
	} else {
		errorCode := resp.Header.Get("Error-Code")
		return metrics.CreateIntelStatusCodeMetric(resp.StatusCode, errorCode), nil
	}

}

type RegistrationService struct {
	intervalDuration    time.Duration
	serverMetrics       *metrics.RegistrationServiceMetricsRegistry
	log                 *slog.Logger
	registrationChecker RegistrationChecker
}

func (r *RegistrationService) Run(ctx context.Context) error {
	err := r.serverMetrics.SetServiceStatusCodeToPending()

	// first run
	r.CheckRegistrationStatus()

	if err != nil {
		return err
	}
	ticker := time.NewTicker(r.intervalDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.CheckRegistrationStatus()
		case <-ctx.Done():
			return nil
		}
	}
}

func (r *RegistrationService) CheckRegistrationStatus() {
	statusCodeMetric, err := r.registrationChecker.Check()
	if err != nil {
		r.log.Error("error getting the registration status", slog.String("err", err.Error()))
	}
	r.log.Debug("Registration check completed", slog.String("status", statusCodeMetric.Status.String()))
	r.serverMetrics.UpdateServiceStatusCodeMetric(statusCodeMetric)

}

func NewRegistrationService(logger *slog.Logger, intervalDuration time.Duration) *RegistrationService {
	registrationService := &RegistrationService{
		intervalDuration:    intervalDuration * time.Minute,
		serverMetrics:       metrics.NewRegistrationServiceMetricsRegistry(logger),
		registrationChecker: NewRegistrationChecker(logger),
		log:                 logger,
	}

	return registrationService
}
