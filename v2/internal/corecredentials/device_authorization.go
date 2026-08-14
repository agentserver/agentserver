package corecredentials

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	AuthorizationMethodManual     = "manual"
	AuthorizationMethodDeviceFlow = "device_flow"
	AuthTypeDeviceOAuth           = "device_oauth"

	DeviceAuthorizationPending   = "pending"
	DeviceAuthorizationSucceeded = "succeeded"
	DeviceAuthorizationDenied    = "denied"
	DeviceAuthorizationExpired   = "expired"
	DeviceAuthorizationFailed    = "failed"
)

// DeviceAuthorizationChallenge contains only the public challenge plus the
// opaque provider state that Core must seal before it is persisted.
type DeviceAuthorizationChallenge struct {
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresAt               time.Time
	Interval                time.Duration
	ProviderState           []byte
	ProviderPublic          json.RawMessage
}

type DeviceAuthorizationPollResult struct {
	Status        string
	RetryAfter    time.Duration
	Credential    UploadResult
	ProviderState []byte
	ErrorCode     string
}

// DeviceAuthorizationProvider is an optional provider capability. Poll is
// deliberately single-shot: scheduling, persistence, leases and retries are
// owned by Core rather than by a request goroutine or a provider SDK loop.
type DeviceAuthorizationProvider interface {
	Provider
	BeginDeviceAuthorization(context.Context, json.RawMessage) (DeviceAuthorizationChallenge, error)
	PollDeviceAuthorization(context.Context, []byte) (DeviceAuthorizationPollResult, error)
}

// RefreshingProvider refreshes a sealed device-OAuth credential. A terminal
// error means the user must authorize again; transient errors keep the old
// still-valid access token and are retried by a later use.
type RefreshingProvider interface {
	Provider
	RefreshDeviceCredential(context.Context, Binding, []byte) (UploadResult, bool, error)
}

func (registry *ProviderRegistry) DeviceAuthorizationProvider(kind string) (DeviceAuthorizationProvider, bool) {
	provider, ok := registry.Lookup(kind)
	if !ok {
		return nil, false
	}
	device, ok := provider.(DeviceAuthorizationProvider)
	if !ok {
		return nil, false
	}
	schema, schemaOK := provider.(SchemaProvider)
	if !schemaOK {
		return nil, false
	}
	for _, method := range schema.Schema().AuthorizationMethods {
		if method == AuthorizationMethodDeviceFlow {
			return device, true
		}
	}
	return nil, false
}

func validateDevicePollStatus(status string) error {
	switch status {
	case DeviceAuthorizationPending, DeviceAuthorizationSucceeded, DeviceAuthorizationDenied,
		DeviceAuthorizationExpired, DeviceAuthorizationFailed:
		return nil
	default:
		return errors.New("device authorization provider returned an invalid status")
	}
}
