// Package objectruntime constructs the production encrypted object store from
// deployment routing configuration. It intentionally has no static credential
// fields: S3 and KMS credentials come only from the AWS SDK workload/default
// credential chain.
package objectruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/agentserver/agentserver/v2/internal/objectstore/awsprovider"
)

const (
	ObjectPrefixEnvironment = "AGENTSERVER_V2_OBJECT_PREFIX"
	S3BucketEnvironment     = "AGENTSERVER_V2_S3_BUCKET"
	S3RegionEnvironment     = "AGENTSERVER_V2_S3_REGION"
	S3EndpointEnvironment   = "AGENTSERVER_V2_S3_ENDPOINT"
	S3PathStyleEnvironment  = "AGENTSERVER_V2_S3_USE_PATH_STYLE"
	KMSRegionEnvironment    = "AGENTSERVER_V2_KMS_REGION"
	KMSEndpointEnvironment  = "AGENTSERVER_V2_KMS_ENDPOINT"
	KMSKeyIDEnvironment     = "AGENTSERVER_V2_KMS_KEY_ID"
	maximumEnvironmentBytes = 4 * 1024
)

// Config contains the complete non-secret authority needed to locate the
// production ciphertext and KMS key. ObjectPrefix is required rather than
// defaulted so a deployment cannot silently fork its durable object namespace.
type Config struct {
	ObjectPrefix string
	Provider     awsprovider.Config
}

// ParseEnvironment reads only agentserver-owned non-secret routing. AWS
// credentials, profiles, web-identity files and metadata endpoints remain the
// responsibility of the SDK's workload/default credential chain.
func ParseEnvironment(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("production object configuration source is required")
	}
	required := func(name string) (string, error) {
		value := getenv(name)
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		if !validEnvironmentValue(value) {
			return "", fmt.Errorf("%s must be bounded UTF-8 text without surrounding whitespace or control bytes", name)
		}
		return value, nil
	}
	optional := func(name string) (string, error) {
		value := getenv(name)
		if value == "" {
			return "", nil
		}
		if !validEnvironmentValue(value) {
			return "", fmt.Errorf("%s must be bounded UTF-8 text without surrounding whitespace or control bytes", name)
		}
		return value, nil
	}

	var config Config
	var err error
	if config.ObjectPrefix, err = required(ObjectPrefixEnvironment); err != nil {
		return Config{}, err
	}
	if err := objectstore.ValidatePrefix(config.ObjectPrefix); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ObjectPrefixEnvironment, err)
	}
	if config.Provider.S3Bucket, err = required(S3BucketEnvironment); err != nil {
		return Config{}, err
	}
	if config.Provider.S3Region, err = required(S3RegionEnvironment); err != nil {
		return Config{}, err
	}
	if config.Provider.S3Endpoint, err = optional(S3EndpointEnvironment); err != nil {
		return Config{}, err
	}
	switch pathStyle := getenv(S3PathStyleEnvironment); pathStyle {
	case "", "false":
		config.Provider.S3UsePathStyle = false
	case "true":
		config.Provider.S3UsePathStyle = true
	default:
		return Config{}, fmt.Errorf("%s must be exactly true or false when present", S3PathStyleEnvironment)
	}
	if config.Provider.KMSRegion, err = required(KMSRegionEnvironment); err != nil {
		return Config{}, err
	}
	if config.Provider.KMSEndpoint, err = optional(KMSEndpointEnvironment); err != nil {
		return Config{}, err
	}
	if config.Provider.KMSKeyID, err = required(KMSKeyIDEnvironment); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Open loads the AWS workload/default configuration and returns the shared
// provider-neutral protocol store. The bound covers every currently supported
// semantic kind; each Core/pool adapter applies its narrower kind-specific
// limit before calling the protocol.
func Open(ctx context.Context, config Config) (*objectstore.Store, error) {
	return open(ctx, config, loadAWSProviders)
}

type providerLoader func(
	context.Context,
	awsprovider.Config,
) (objectstore.ImmutableBlobStore, objectstore.DataKeyProvider, error)

func loadAWSProviders(
	ctx context.Context,
	config awsprovider.Config,
) (objectstore.ImmutableBlobStore, objectstore.DataKeyProvider, error) {
	providers, err := awsprovider.Load(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	return providers.Blobs, providers.Keys, nil
}

func open(ctx context.Context, config Config, load providerLoader) (*objectstore.Store, error) {
	if ctx == nil {
		return nil, errors.New("production object store context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := objectstore.ValidatePrefix(config.ObjectPrefix); err != nil {
		return nil, fmt.Errorf("%s: %w", ObjectPrefixEnvironment, err)
	}
	if load == nil {
		return nil, errors.New("production object provider loader is required")
	}
	blobs, keys, err := load(ctx, config.Provider)
	if err != nil {
		return nil, fmt.Errorf("load production AWS object providers: %w", err)
	}
	store, err := objectstore.New(objectstore.Config{
		Backend: blobs, Keys: keys, Prefix: config.ObjectPrefix,
		MaximumPlaintextBytes: checkpoint.MaximumArtifactBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("configure production encrypted object protocol: %w", err)
	}
	return store, nil
}

func validEnvironmentValue(value string) bool {
	if len(value) < 1 || len(value) > maximumEnvironmentBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
