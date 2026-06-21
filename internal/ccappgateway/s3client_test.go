package ccappgateway_test

import (
	"context"
	"testing"

	"github.com/agentserver/agentserver/internal/ccappgateway"
)

func TestNewS3Client_RegionRequired(t *testing.T) {
	_, err := ccappgateway.NewS3Client(context.Background(), ccappgateway.S3Config{
		Bucket: "test-bucket",
	})
	if err == nil {
		t.Fatal("NewS3Client should fail without Region")
	}
}

func TestNewS3Client_BucketRequired(t *testing.T) {
	_, err := ccappgateway.NewS3Client(context.Background(), ccappgateway.S3Config{
		Region: "us-east-1",
	})
	if err == nil {
		t.Fatal("NewS3Client should fail without Bucket")
	}
}

func TestNewS3Client_ValidConfig(t *testing.T) {
	// Set dummy AWS env so config.LoadDefaultConfig doesn't error reaching for IRSA.
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	store, err := ccappgateway.NewS3Client(context.Background(), ccappgateway.S3Config{
		Region: "us-east-1",
		Bucket: "test-bucket",
	})
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	if store == nil {
		t.Fatal("NewS3Client returned nil store")
	}
}
