package awsprovider

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	maximumS3ObjectKeyBytes = 1024
	maximumS3PutObjectBytes = int64(5 * 1024 * 1024 * 1024)
)

type s3API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3BlobStore implements the immutable ciphertext boundary with conditional
// single-part PutObject. The SDK client used by Load has retries disabled so an
// opaque transport failure is surfaced for exact reconciliation by objectstore.
type S3BlobStore struct {
	client s3API
	bucket string
}

func NewS3BlobStore(client s3API, bucket string) (*S3BlobStore, error) {
	if client == nil {
		return nil, errors.New("S3 object client is required")
	}
	if !validProviderText(bucket, maximumProviderIdentifierBytes) {
		return nil, errors.New("S3 object bucket is required and must be bounded printable text")
	}
	return &S3BlobStore{client: client, bucket: bucket}, nil
}

func (store *S3BlobStore) PutIfAbsent(
	ctx context.Context,
	key string,
	size int64,
	source io.Reader,
) (objectstore.PutResult, error) {
	if ctx == nil {
		return objectstore.PutResult{}, errors.New("S3 conditional put context is required")
	}
	if err := ctx.Err(); err != nil {
		return objectstore.PutResult{}, err
	}
	if store == nil || store.client == nil || store.bucket == "" {
		return objectstore.PutResult{}, errors.New("S3 object store is not initialized")
	}
	if !validProviderText(key, maximumS3ObjectKeyBytes) || size < 1 || size > maximumS3PutObjectBytes || source == nil {
		return objectstore.PutResult{}, errors.New("S3 conditional put requires a bounded key, supported positive size, and source")
	}
	body := &exactLengthReader{source: source, remaining: size}
	output, err := store.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(store.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		IfNoneMatch:   aws.String("*"),
	})
	if err != nil {
		if isS3PreconditionFailed(err) {
			return objectstore.PutResult{Created: false}, nil
		}
		return objectstore.PutResult{}, fmt.Errorf("S3 conditional put %q: %w", key, err)
	}
	if output == nil {
		return objectstore.PutResult{}, errors.New("S3 conditional put returned a nil success output")
	}
	if body.remaining != 0 {
		return objectstore.PutResult{}, fmt.Errorf(
			"S3 conditional put returned success after consuming %d of %d bytes",
			size-body.remaining, size,
		)
	}
	return objectstore.PutResult{Created: true}, nil
}

func (store *S3BlobStore) Open(ctx context.Context, key string) (objectstore.Blob, error) {
	if ctx == nil {
		return objectstore.Blob{}, errors.New("S3 object open context is required")
	}
	if err := ctx.Err(); err != nil {
		return objectstore.Blob{}, err
	}
	if store == nil || store.client == nil || store.bucket == "" {
		return objectstore.Blob{}, errors.New("S3 object store is not initialized")
	}
	if !validProviderText(key, maximumS3ObjectKeyBytes) {
		return objectstore.Blob{}, errors.New("S3 object key is required and must be bounded printable text")
	}
	output, err := store.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		closeErr := closeS3Output(output)
		wrapped := fmt.Errorf("S3 open object %q: %w", key, err)
		if isS3NoSuchKey(err) {
			return objectstore.Blob{}, errors.Join(objectstore.ErrBlobNotFound, wrapped, closeErr)
		}
		return objectstore.Blob{}, errors.Join(wrapped, closeErr)
	}
	if output == nil {
		return objectstore.Blob{}, errors.New("S3 open object returned a nil success output")
	}
	if output.Body == nil {
		return objectstore.Blob{}, errors.New("S3 open object returned a nil body")
	}
	if output.ContentLength == nil || *output.ContentLength < 1 {
		return objectstore.Blob{}, errors.Join(
			errors.New("S3 open object returned an invalid content length"),
			output.Body.Close(),
		)
	}
	return objectstore.Blob{Reader: output.Body, Size: *output.ContentLength}, nil
}

func isS3PreconditionFailed(err error) bool {
	return hasS3APIErrorCode(err, "PreconditionFailed") && hasHTTPStatusCode(err, 412)
}

func isS3NoSuchKey(err error) bool {
	var typed *s3types.NoSuchKey
	return errors.As(err, &typed) || hasS3APIErrorCode(err, "NoSuchKey")
}

func hasS3APIErrorCode(err error, code string) bool {
	var apiError smithy.APIError
	return errors.As(err, &apiError) && apiError.ErrorCode() == code
}

func hasHTTPStatusCode(err error, status int) bool {
	var responseError interface{ HTTPStatusCode() int }
	return errors.As(err, &responseError) && responseError.HTTPStatusCode() == status
}

func closeS3Output(output *s3.GetObjectOutput) error {
	if output == nil || output.Body == nil {
		return nil
	}
	return output.Body.Close()
}

type exactLengthReader struct {
	source    io.Reader
	remaining int64
}

func (reader *exactLengthReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(destination)) > reader.remaining {
		destination = destination[:reader.remaining]
	}
	written, err := reader.source.Read(destination)
	if int64(written) > reader.remaining {
		return 0, errors.New("S3 conditional put source violated io.Reader bounds")
	}
	reader.remaining -= int64(written)
	return written, err
}

var _ objectstore.ImmutableBlobStore = (*S3BlobStore)(nil)
