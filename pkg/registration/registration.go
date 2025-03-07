package registration

import (
	"context"
	"log"
	"log/slog"
	"time"

	mpmanagement "github.com/opensovereigncloud/cc-intel-platform-registration/internal/pkg/mp_management"
	sgxplatforminfo "github.com/opensovereigncloud/cc-intel-platform-registration/internal/pkg/sgx_platform_info"
	intelservices "github.com/opensovereigncloud/cc-intel-platform-registration/pkg/intel_services"
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
	mp, err := mpmanagement.NewMPManagement()
	intelService := intelservices.NewIntelService(rc.log)

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

		metric, regErr := intelService.RegisterPlatform(plaformManifest)

		// registration was successful
		if metric.Status == metrics.PlatformRebootNeeded {
			regStatusErr := mp.CompleteMachineRegistrationStatus()
			if regStatusErr != nil {
				rc.log.Error("unable to set registration status UEFI variable as complete ", slog.String("error", err.Error()))
				return metrics.StatusCodeMetric{Status: metrics.UefiPersistFailed}, nil
			}
		}
		return metric, regErr

	}

	platformInfo, err := sgxplatforminfo.GetSgxPcePlatformInfo()
	if err != nil {
		rc.log.Error("unable to get platform info", slog.String("error", err.Error()))
		return metrics.StatusCodeMetric{Status: metrics.RetryNeeded}, nil
	}

	metric, err := intelService.RetrievePCK(platformInfo)
	return metric, err
}

type RegistrationService struct {
	intervalDuration    time.Duration
	serverMetrics       *metrics.RegistrationServiceMetricsRegistry
	log                 *slog.Logger
	registrationChecker RegistrationChecker
}

func (r *RegistrationService) Run(ctx context.Context) error {
	err := r.serverMetrics.SetServiceStatusCodeToPending()

	// first check
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
