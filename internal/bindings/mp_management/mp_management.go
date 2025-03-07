package mp_management

/*
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -L../../../third_party/mp_management/lib -lmp_management -lstdc++

#include <stdlib.h>
#include "../../../third_party/mp_management/src/include/c_wrapper/mp_management.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	MaxPathSize     = 256
	MaxResponseSize = 1024 * 30
	MaxRequestSize  = 1024 * 56
	MaxDataSize     = 1024 * 30
)

// Result codes
const (
	MPSuccess             = 0
	MPNoPendingData       = 1
	MPAlreadyRegistered   = 2
	MPMemError            = 3
	MPUefiInternalError   = 4
	MPUserInsufficientMem = 5
	MPInvalidParameter    = 6
	MPSgxNotSupported     = 7
	MPUnexpectedError     = 8
	MPRedundantOperation  = 9
	MPNetworkError        = 10
	MPNotInitialized      = 11
)

// TaskStatus constants
const (
	MPTaskInProgress = 0
	MPTaskCompleted  = 1
)

// MPManagement represents the Go wrapper for the mp_management functions
type MPManagement struct {
	initialized bool
}

type PlatformManifest []byte

// NewMPManagement creates a new instance of MPManagement
func NewMPManagement(uefiPath string) (*MPManagement, error) {
	cPath := C.CString(uefiPath)
	defer C.free(unsafe.Pointer(cPath))

	C.mp_management_init(cPath)

	return &MPManagement{initialized: true}, nil
}

// Close terminates the MPManagement instance
func (mp *MPManagement) Close() {
	if mp.initialized {
		C.mp_management_terminate()
		mp.initialized = false
	}
}

// GetPlatformManifest retrieves the platform manifest
func (mp *MPManagement) GetPlatformManifest() (PlatformManifest, error) {
	var size C.uint16_t
	result := C.mp_management_get_platform_manifest(nil, &size)

	if result != MPSuccess && result != MPUserInsufficientMem {
		return nil, fmt.Errorf("failed to get buffer size: %d", result)
	}

	buffer := make([]byte, size)
	result = C.mp_management_get_platform_manifest((*C.uint8_t)(&buffer[0]), &size)

	if result != MPSuccess {
		return nil, fmt.Errorf("failed to get platform manifest: %d", result)
	}

	return buffer, nil
}

// GetRegistrationStatus retrieves the registration status
func (mp *MPManagement) GetRegistrationStatus() (bool, error) {
	var status C.MpTaskStatus
	result := C.mp_management_get_registration_status(&status)
	if result != MPSuccess {
		return false, fmt.Errorf("failed to get registration status: %d", result)
	}
	return true, nil
}

// GetRegistrationStatus retrieves the registration status
func (mp *MPManagement) SetRegistrationStatusAsComplete() error {
	var status C.MpTaskStatus
	result := C.mp_management_set_registration_status_as_complete(&status)
	if result != MPSuccess {
		return fmt.Errorf("failed to get registration status: %d", result)
	}
	return nil
}
