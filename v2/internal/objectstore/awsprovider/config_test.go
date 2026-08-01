package awsprovider

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestProviderConfigValidation(t *testing.T) {
	valid := testProviderConfig()
	if err := validateConfig(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing bucket", mutate: func(config *Config) { config.S3Bucket = "" }},
		{name: "missing S3 region", mutate: func(config *Config) { config.S3Region = "" }},
		{name: "missing KMS region", mutate: func(config *Config) { config.KMSRegion = "" }},
		{name: "invalid signing region", mutate: func(config *Config) { config.S3Region = "us east 1" }},
		{name: "missing KMS key", mutate: func(config *Config) { config.KMSKeyID = "" }},
		{name: "oversize KMS key", mutate: func(config *Config) { config.KMSKeyID = strings.Repeat("k", maximumKMSKeyIDBytes+1) }},
		{name: "whitespace", mutate: func(config *Config) { config.S3Bucket = " objects" }},
		{name: "cleartext S3 endpoint", mutate: func(config *Config) { config.S3Endpoint = "http://s3.example.test" }},
		{name: "endpoint userinfo", mutate: func(config *Config) { config.S3Endpoint = "https://user@s3.example.test" }},
		{name: "endpoint query", mutate: func(config *Config) { config.KMSEndpoint = "https://kms.example.test?key=value" }},
		{name: "endpoint fragment", mutate: func(config *Config) { config.KMSEndpoint = "https://kms.example.test#fragment" }},
		{name: "empty endpoint fragment", mutate: func(config *Config) { config.KMSEndpoint = "https://kms.example.test#" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if err := validateConfig(config); err == nil {
				t.Fatalf("validateConfig(%+v) succeeded", config)
			}
		})
	}
}

func TestProviderConfigContainsOnlyNonSecretRouting(t *testing.T) {
	typeOfConfig := reflect.TypeFor[Config]()
	fields := make([]string, 0, typeOfConfig.NumField())
	for index := range typeOfConfig.NumField() {
		fields = append(fields, typeOfConfig.Field(index).Name)
	}
	want := []string{"S3Bucket", "S3Region", "S3Endpoint", "S3UsePathStyle", "KMSRegion", "KMSEndpoint", "KMSKeyID"}
	if !slices.Equal(fields, want) {
		t.Fatalf("provider Config fields = %v, want non-secret routing only %v", fields, want)
	}
}

func TestProviderSanitizesAmbientEndpointsWithoutDroppingWorkloadIdentity(t *testing.T) {
	credentials := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "workload"}, nil
	})
	original := aws.Config{
		Region: "ambient", Credentials: credentials,
		BaseEndpoint: aws.String("https://ambient.invalid"), ConfigSources: []any{"ambient"},
	}
	sanitized := sanitizeSDKConfig(original)
	if sanitized.BaseEndpoint != nil || sanitized.ConfigSources != nil || sanitized.Credentials == nil || sanitized.Region != "ambient" {
		t.Fatalf("sanitized AWS config = %+v", sanitized)
	}
	if original.BaseEndpoint == nil || len(original.ConfigSources) != 1 {
		t.Fatal("sanitizeSDKConfig mutated the caller's AWS config")
	}
}

func TestS3ClientOptionsDisableOpaqueRetriesAndAmbientChecksums(t *testing.T) {
	config := testProviderConfig()
	options := s3.Options{
		Region:                     "ambient-region",
		BaseEndpoint:               aws.String("https://ambient.invalid"),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenSupported,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenSupported,
	}
	s3ClientOptions(config)(&options)
	if options.Region != config.S3Region || aws.ToString(options.BaseEndpoint) != config.S3Endpoint ||
		!options.UsePathStyle || options.Retryer == nil || options.Retryer.MaxAttempts() != 1 ||
		options.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired ||
		options.ResponseChecksumValidation != aws.ResponseChecksumValidationWhenRequired {
		t.Fatalf("S3 options = %+v", options)
	}
	if _, ok := options.Retryer.(aws.NopRetryer); !ok {
		t.Fatalf("S3 retryer type = %T", options.Retryer)
	}

	config.S3Endpoint = ""
	s3ClientOptions(config)(&options)
	if options.BaseEndpoint != nil {
		t.Fatalf("empty configured S3 endpoint retained ambient endpoint %q", aws.ToString(options.BaseEndpoint))
	}
}

func TestKMSClientOptionsUseIndependentRegionAndEndpoint(t *testing.T) {
	config := testProviderConfig()
	options := kms.Options{Region: "ambient", BaseEndpoint: aws.String("https://ambient.invalid")}
	kmsClientOptions(config)(&options)
	if options.Region != config.KMSRegion || aws.ToString(options.BaseEndpoint) != config.KMSEndpoint {
		t.Fatalf("KMS options = %+v", options)
	}
	config.KMSEndpoint = ""
	kmsClientOptions(config)(&options)
	if options.BaseEndpoint != nil {
		t.Fatalf("empty configured KMS endpoint retained ambient endpoint %q", aws.ToString(options.BaseEndpoint))
	}
}

func TestProviderLoadRejectsNilAndCancelledContext(t *testing.T) {
	if _, err := Load(nil, testProviderConfig()); err == nil {
		t.Fatal("Load accepted nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(ctx, testProviderConfig()); err == nil {
		t.Fatal("Load accepted cancelled context")
	}
}

func testProviderConfig() Config {
	return Config{
		S3Bucket: "agentserver-production-objects", S3Region: "us-east-1",
		S3Endpoint: "https://s3.example.test/storage", S3UsePathStyle: true,
		KMSRegion: "us-west-2", KMSEndpoint: "https://kms.example.test",
		KMSKeyID: "arn:aws:kms:us-west-2:111122223333:key/00000000-0000-4000-8000-000000000001",
	}
}
