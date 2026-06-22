package ccappgateway

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// S3Config bundles the S3 connection settings. AWS credentials are sourced
// from the SDK default chain (env vars, IRSA tokens, shared config, EC2/ECS
// instance metadata) — NOT explicit static creds. This is required for prod
// EKS deployments where IRSA tokens rotate automatically.
type S3Config struct {
	Endpoint  string // optional: MinIO/dev endpoint URL
	Region    string // required
	Bucket    string // required
	PathStyle bool   // true for MinIO; false for real AWS
}

// NewS3Client constructs a workspace.ObjectStore backed by aws-sdk-go-v2.
// Validates Region and Bucket are non-empty. Returns wrapped errors on
// AWS config load failure (e.g., missing credentials in dev where the
// default chain has nothing to find).
func NewS3Client(ctx context.Context, cfg S3Config) (workspace.ObjectStore, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("s3client: Region required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3client: Bucket required")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("s3client: load aws config: %w", err)
	}

	opts := []func(*s3.Options){}
	if cfg.Endpoint != "" {
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}
	if cfg.PathStyle {
		opts = append(opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, opts...)
	return &s3Store{client: client, bucket: cfg.Bucket}, nil
}

type s3Store struct {
	client *s3.Client
	bucket string
}

func (s *s3Store) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytesReader(data),
	})
	return err
}

func (s *s3Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, workspace.ErrObjectNotFound
		}
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// bytesReader wraps []byte as a ReadSeeker (S3 PutObject requires Seeker).
func bytesReader(b []byte) io.ReadSeeker { return &byteReadSeeker{b: b} }

type byteReadSeeker struct {
	b []byte
	p int64
}

func (r *byteReadSeeker) Read(p []byte) (int, error) {
	if r.p >= int64(len(r.b)) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.p:])
	r.p += int64(n)
	return n, nil
}

func (r *byteReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.p + offset
	case io.SeekEnd:
		abs = int64(len(r.b)) + offset
	default:
		return 0, fmt.Errorf("invalid whence %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("negative position")
	}
	r.p = abs
	return abs, nil
}
