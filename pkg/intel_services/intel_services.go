package intelservices

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/opensovereigncloud/cc-intel-platform-registration/pkg/constants"

	mpmanagement "github.com/opensovereigncloud/cc-intel-platform-registration/internal/pkg/mp_management"
	sgxplatforminfo "github.com/opensovereigncloud/cc-intel-platform-registration/internal/pkg/sgx_platform_info"
	"github.com/opensovereigncloud/cc-intel-platform-registration/pkg/metrics"
)

type IntelService struct {
	log *slog.Logger
}

func NewIntelService(logger *slog.Logger) *IntelService {
	return &IntelService{
		log: logger,
	}
}

func createIntelStatusCodeMetricForPlatformRegistration(httpStatusCode int, intelErrorCode string) metrics.StatusCodeMetric {
	var Status metrics.StatusCode
	if httpStatusCode >= 400 && httpStatusCode < 500 {
		Status = metrics.InvalidRegistrationRequest
	} else {
		Status = metrics.IntelRegServiceRequestFailed
	}
	return metrics.StatusCodeMetric{
		Status:         Status,
		HttpStatusCode: strconv.Itoa(httpStatusCode),
		IntelError:     intelErrorCode,
	}
}

func createIntelStatusCodeMetricForDirectRegistration(httpStatusCode int, intelErrorCode string) metrics.StatusCodeMetric {

	var Status metrics.StatusCode
	if httpStatusCode == 404 {
		Status = metrics.SgxResetNeeded
	} else {
		Status = metrics.RetryNeeded
	}

	return metrics.StatusCodeMetric{
		Status:         Status,
		HttpStatusCode: strconv.Itoa(httpStatusCode),
		IntelError:     intelErrorCode,
	}
}

func (r *IntelService) RegisterPlatform(platformManifest mpmanagement.PlatformManifest) (metrics.StatusCodeMetric, error) {
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
		return createIntelStatusCodeMetricForPlatformRegistration(resp.StatusCode, errorCode), nil
	}

}

func (r *IntelService) RetrievePCK(platformInfo *sgxplatforminfo.SgxPcePlatformInfo) (metrics.StatusCodeMetric, error) {
	ctx, cancel := context.WithTimeout(context.TODO(), time.Duration(constants.IntelRegirationRequestTimeoutInMinutes*time.Minute))
	defer cancel()
	client := &http.Client{}

	requestURL := fmt.Sprintf("%s?encrypted_ppid=%s&pceid=%s",
		constants.IntelPckRetrievalEndpoint, platformInfo.EncryptedPPID, platformInfo.PCEInfo.PCEID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)

	if err != nil {
		r.log.Error("failed to create request", slog.String("error", err.Error()))
		return metrics.CreateUnknownErrorStatusCodeMetric(), fmt.Errorf("failed to create request: %w", err)
	}

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

	if resp.StatusCode == 200 {
		return metrics.StatusCodeMetric{Status: metrics.PlatformDirectlyRegistered}, nil
	} else {
		errorCode := resp.Header.Get("Error-Code")
		return createIntelStatusCodeMetricForDirectRegistration(resp.StatusCode, errorCode), nil
	}

}
