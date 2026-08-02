// Package objectruntime constructs the deliberately plaintext production
// object store for an S3-compatible service. Credentials are explicit inputs;
// KMS, STS and ambient AWS credential discovery are not part of this profile.
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
	"github.com/aws/aws-sdk-go-v2/credentials"
)

const (
	ObjectPrefixEnvironment = "AGENTSERVER_V2_OBJECT_PREFIX"
	S3BucketEnvironment     = "AGENTSERVER_V2_S3_BUCKET"
	S3RegionEnvironment     = "AGENTSERVER_V2_S3_REGION"
	S3EndpointEnvironment   = "AGENTSERVER_V2_S3_ENDPOINT"
	S3PathStyleEnvironment  = "AGENTSERVER_V2_S3_USE_PATH_STYLE"
	S3AccessKeyEnvironment  = "AGENTSERVER_V2_S3_ACCESS_KEY_ID"
	S3SecretKeyEnvironment  = "AGENTSERVER_V2_S3_SECRET_ACCESS_KEY"
	maximumEnvironmentBytes = 4 * 1024
)

// Config contains the complete authority and credential needed to locate the
// production plaintext objects. ObjectPrefix is required rather than defaulted
// so a deployment cannot silently fork its durable object namespace.
type Config struct {
	ObjectPrefix    string
	Provider        awsprovider.S3Config
	AccessKeyID     string
	SecretAccessKey string
}

// ParseEnvironment reads agentserver-owned routing and exact Secret-backed S3
// credentials. It does not consult profiles, web identity or metadata services.
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
	if config.Provider.Bucket, err = required(S3BucketEnvironment); err != nil {
		return Config{}, err
	}
	if config.Provider.Region, err = required(S3RegionEnvironment); err != nil {
		return Config{}, err
	}
	if config.Provider.Endpoint, err = optional(S3EndpointEnvironment); err != nil {
		return Config{}, err
	}
	switch pathStyle := getenv(S3PathStyleEnvironment); pathStyle {
	case "", "false":
		config.Provider.UsePathStyle = false
	case "true":
		config.Provider.UsePathStyle = true
	default:
		return Config{}, fmt.Errorf("%s must be exactly true or false when present", S3PathStyleEnvironment)
	}
	if config.AccessKeyID, err = required(S3AccessKeyEnvironment); err != nil {
		return Config{}, err
	}
	if config.SecretAccessKey, err = required(S3SecretKeyEnvironment); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Open constructs the shared plaintext protocol store. The bound covers every
// currently supported semantic kind; each Core/pool adapter applies its
// narrower kind-specific limit before calling the protocol.
func Open(ctx context.Context, config Config) (*objectstore.PlainStore, error) {
	return open(ctx, config, loadS3Provider)
}

type providerLoader func(
	context.Context,
	awsprovider.S3Config,
	string,
	string,
) (objectstore.ImmutableBlobStore, error)

func loadS3Provider(
	ctx context.Context,
	config awsprovider.S3Config,
	accessKeyID string,
	secretAccessKey string,
) (objectstore.ImmutableBlobStore, error) {
	return awsprovider.LoadS3(ctx, config, credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""))
}

func open(ctx context.Context, config Config, load providerLoader) (*objectstore.PlainStore, error) {
	if ctx == nil {
		return nil, errors.New("production object store context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := objectstore.ValidatePrefix(config.ObjectPrefix); err != nil {
		return nil, fmt.Errorf("%s: %w", ObjectPrefixEnvironment, err)
	}
	if err := awsprovider.ValidateS3Config(config.Provider); err != nil {
		return nil, err
	}
	if !validEnvironmentValue(config.AccessKeyID) || !validEnvironmentValue(config.SecretAccessKey) {
		return nil, errors.New("production S3 credentials are missing or invalid")
	}
	if load == nil {
		return nil, errors.New("production object provider loader is required")
	}
	blobs, err := load(ctx, config.Provider, config.AccessKeyID, config.SecretAccessKey)
	if err != nil {
		return nil, fmt.Errorf("load production S3 object provider: %w", err)
	}
	store, err := objectstore.NewPlain(objectstore.PlainConfig{
		Backend: blobs, Prefix: config.ObjectPrefix,
		MaximumPlaintextBytes: checkpoint.MaximumArtifactBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("configure production plaintext object protocol: %w", err)
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
