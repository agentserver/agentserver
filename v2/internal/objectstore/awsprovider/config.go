// Package awsprovider is the reference AWS SDK v2 transport for the
// provider-neutral encrypted object protocol. S3 stores only application
// ciphertext and KMS only wraps per-object data keys.
package awsprovider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	maximumProviderIdentifierBytes = 2 * 1024
	maximumProviderEndpointBytes   = 4 * 1024
)

var providerRegionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)

// Config contains only non-secret provider routing. Credentials are resolved
// exclusively through the AWS SDK default credential chain; static access-key
// fields intentionally do not exist here.
type Config struct {
	S3Bucket       string
	S3Region       string
	S3Endpoint     string
	S3UsePathStyle bool
	KMSRegion      string
	KMSEndpoint    string
	KMSKeyID       string
}

type Providers struct {
	Blobs *S3BlobStore
	Keys  *KMSDataKeyProvider
}

// Load resolves workload/default credentials once, then constructs separate
// S3 and KMS clients with independently configured regions and endpoints.
func Load(ctx context.Context, config Config) (Providers, error) {
	if ctx == nil {
		return Providers{}, errors.New("AWS object provider context is required")
	}
	if err := ctx.Err(); err != nil {
		return Providers{}, err
	}
	if err := validateConfig(config); err != nil {
		return Providers{}, err
	}

	sdkConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(config.S3Region))
	if err != nil {
		return Providers{}, fmt.Errorf("load AWS SDK workload configuration: %w", err)
	}
	// Endpoint routing is application configuration, not ambient AWS endpoint
	// environment. Credential, CA and HTTP-client resolution has already been
	// completed by LoadDefaultConfig.
	sdkConfig = sanitizeSDKConfig(sdkConfig)

	s3Config := sdkConfig.Copy()
	s3Config.Region = config.S3Region
	s3Client := s3.NewFromConfig(s3Config, s3ClientOptions(config))
	blobs, err := NewS3BlobStore(s3Client, config.S3Bucket)
	if err != nil {
		return Providers{}, err
	}

	kmsConfig := sdkConfig.Copy()
	kmsConfig.Region = config.KMSRegion
	kmsClient := kms.NewFromConfig(kmsConfig, kmsClientOptions(config))
	keys, err := NewKMSDataKeyProvider(kmsClient, config.KMSKeyID)
	if err != nil {
		return Providers{}, err
	}
	return Providers{Blobs: blobs, Keys: keys}, nil
}

func sanitizeSDKConfig(config aws.Config) aws.Config {
	config.BaseEndpoint = nil
	config.ConfigSources = nil
	return config
}

func validateConfig(config Config) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "S3 bucket", value: config.S3Bucket},
		{name: "S3 region", value: config.S3Region},
		{name: "KMS region", value: config.KMSRegion},
		{name: "KMS key ID", value: config.KMSKeyID},
	} {
		if !validProviderText(field.value, maximumProviderIdentifierBytes) {
			return fmt.Errorf("%s is required and must be bounded printable text", field.name)
		}
	}
	if !providerRegionPattern.MatchString(config.S3Region) || !providerRegionPattern.MatchString(config.KMSRegion) {
		return errors.New("S3 and KMS regions must be canonical signing-region names")
	}
	if !validProviderText(config.KMSKeyID, maximumKMSKeyIDBytes) {
		return errors.New("KMS key ID exceeds the encrypted object header bound")
	}
	if err := validateEndpoint("S3 endpoint", config.S3Endpoint); err != nil {
		return err
	}
	if err := validateEndpoint("KMS endpoint", config.KMSEndpoint); err != nil {
		return err
	}
	return nil
}

func validateEndpoint(name, raw string) error {
	if raw == "" {
		return nil
	}
	if !validProviderText(raw, maximumProviderEndpointBytes) {
		return fmt.Errorf("%s must be bounded printable text", name)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" ||
		strings.Contains(raw, "#") {
		return fmt.Errorf("%s must be an absolute HTTPS URL without userinfo, query, or fragment", name)
	}
	return nil
}

func validProviderText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func s3ClientOptions(config Config) func(*s3.Options) {
	return func(options *s3.Options) {
		options.Region = config.S3Region
		options.BaseEndpoint = optionalString(config.S3Endpoint)
		options.UsePathStyle = config.S3UsePathStyle
		options.Retryer = aws.NopRetryer{}
		options.RetryMaxAttempts = 1
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		options.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	}
}

func kmsClientOptions(config Config) func(*kms.Options) {
	return func(options *kms.Options) {
		options.Region = config.KMSRegion
		options.BaseEndpoint = optionalString(config.KMSEndpoint)
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return aws.String(value)
}
