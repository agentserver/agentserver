package awsprovider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestS3BlobStoreConditionalPut(t *testing.T) {
	contents := []byte("complete encrypted object")
	var captured *s3.PutObjectInput
	client := &fakeS3Client{
		put: func(_ context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			captured = input
			got, err := io.ReadAll(input.Body)
			if err != nil {
				t.Fatalf("read PutObject body: %v", err)
			}
			if !bytes.Equal(got, contents) {
				t.Fatalf("PutObject body = %q", got)
			}
			return &s3.PutObjectOutput{}, nil
		},
	}
	store, err := NewS3BlobStore(client, "production-objects")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.PutIfAbsent(t.Context(), "prefix/workspace/checkpoint/object", int64(len(contents)), bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Fatal("conditional put did not report creation")
	}
	if captured == nil || aws.ToString(captured.Bucket) != "production-objects" ||
		aws.ToString(captured.Key) != "prefix/workspace/checkpoint/object" ||
		aws.ToString(captured.IfNoneMatch) != "*" || aws.ToInt64(captured.ContentLength) != int64(len(contents)) {
		t.Fatalf("PutObject input = %+v", captured)
	}
}

func TestS3SDKConditionalWireAndErrorClassification(t *testing.T) {
	t.Run("success wire", func(t *testing.T) {
		contents := []byte("ciphertext sent through the real SDK serializer")
		calls := 0
		client := newTestS3SDKClient(t, func(request *http.Request) (*http.Response, error) {
			calls++
			got, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read serialized request body: %v", err)
			}
			if request.Method != http.MethodPut || request.URL.Path != "/objects/prefix/object" ||
				request.Header.Get("If-None-Match") != "*" || request.ContentLength != int64(len(contents)) ||
				!bytes.Equal(got, contents) {
				t.Fatalf("serialized request = %s %s length=%d headers=%v body=%q",
					request.Method, request.URL, request.ContentLength, request.Header, got)
			}
			return s3SDKResponse(request, http.StatusOK, ""), nil
		})
		store, err := NewS3BlobStore(client, "objects")
		if err != nil {
			t.Fatal(err)
		}
		result, err := store.PutIfAbsent(t.Context(), "prefix/object", int64(len(contents)), bytes.NewReader(contents))
		if err != nil || !result.Created || calls != 1 {
			t.Fatalf("PutIfAbsent() = %+v, %v; calls %d", result, err, calls)
		}
	})

	tests := []struct {
		name        string
		status      int
		code        string
		wantExists  bool
		wantMissing bool
		open        bool
	}{
		{name: "412 precondition", status: http.StatusPreconditionFailed, code: "PreconditionFailed", wantExists: true},
		{name: "409 remains ambiguous", status: http.StatusConflict, code: "ConditionalRequestConflict"},
		{name: "500 is not retried", status: http.StatusInternalServerError, code: "InternalError"},
		{name: "NoSuchKey read", status: http.StatusNotFound, code: "NoSuchKey", wantMissing: true, open: true},
		{name: "NoSuchBucket read", status: http.StatusNotFound, code: "NoSuchBucket", open: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := newTestS3SDKClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				return s3SDKResponse(request, test.status, s3ErrorXML(test.code)), nil
			})
			store, err := NewS3BlobStore(client, "objects")
			if err != nil {
				t.Fatal(err)
			}
			if test.open {
				_, openErr := store.Open(t.Context(), "object")
				if openErr == nil || errors.Is(openErr, objectstore.ErrBlobNotFound) != test.wantMissing || calls != 1 {
					t.Fatalf("Open() = %v; missing %v; calls %d", openErr, errors.Is(openErr, objectstore.ErrBlobNotFound), calls)
				}
				return
			}
			result, putErr := store.PutIfAbsent(t.Context(), "object", 1, bytes.NewReader([]byte{1}))
			if test.wantExists {
				if putErr != nil || result.Created || calls != 1 {
					t.Fatalf("PutIfAbsent() = %+v, %v; calls %d", result, putErr, calls)
				}
				return
			}
			if putErr == nil || result.Created || calls != 1 {
				t.Fatalf("PutIfAbsent() = %+v, %v; calls %d", result, putErr, calls)
			}
		})
	}
}

func TestS3BlobStoreOnlyMapsExplicitPreconditionFailure(t *testing.T) {
	tests := []struct {
		name        string
		providerErr error
		wantExists  bool
	}{
		{name: "precondition failed", providerErr: &s3TestAPIError{code: "PreconditionFailed", status: 412}, wantExists: true},
		{name: "conditional conflict remains ambiguous", providerErr: &s3TestAPIError{code: "ConditionalRequestConflict", status: 409}},
		{name: "wrong code at 412", providerErr: &s3TestAPIError{code: "AccessDenied", status: 412}},
		{name: "wrong status for code", providerErr: &s3TestAPIError{code: "PreconditionFailed", status: 500}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeS3Client{put: func(context.Context, *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
				return nil, test.providerErr
			}}
			store, err := NewS3BlobStore(client, "objects")
			if err != nil {
				t.Fatal(err)
			}
			result, putErr := store.PutIfAbsent(t.Context(), "object", 1, bytes.NewReader([]byte{1}))
			if test.wantExists {
				if putErr != nil || result.Created {
					t.Fatalf("PutIfAbsent() = %+v, %v", result, putErr)
				}
				return
			}
			if putErr == nil || result.Created || !errors.Is(putErr, test.providerErr) {
				t.Fatalf("PutIfAbsent() = %+v, %v", result, putErr)
			}
		})
	}
}

func TestS3BlobStoreRejectsSuccessBeforeBodyConsumption(t *testing.T) {
	client := &fakeS3Client{put: func(context.Context, *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		return &s3.PutObjectOutput{}, nil
	}}
	store, err := NewS3BlobStore(client, "objects")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutIfAbsent(t.Context(), "object", 4, bytes.NewReader([]byte("data"))); err == nil {
		t.Fatal("PutIfAbsent accepted a client success that did not consume the body")
	}

	client.put = func(_ context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		_, _ = io.Copy(io.Discard, input.Body)
		return &s3.PutObjectOutput{}, nil
	}
	if _, err := store.PutIfAbsent(t.Context(), "object", 5, bytes.NewReader([]byte("data"))); err == nil {
		t.Fatal("PutIfAbsent accepted a short body")
	}
}

func TestS3BlobStoreOpen(t *testing.T) {
	body := &trackingReadCloser{Reader: bytes.NewReader([]byte("ciphertext"))}
	var captured *s3.GetObjectInput
	client := &fakeS3Client{get: func(_ context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		captured = input
		return &s3.GetObjectOutput{Body: body, ContentLength: aws.Int64(10)}, nil
	}}
	store, err := NewS3BlobStore(client, "production-objects")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := store.Open(t.Context(), "prefix/object")
	if err != nil {
		t.Fatal(err)
	}
	if blob.Reader != body || blob.Size != 10 || aws.ToString(captured.Bucket) != "production-objects" ||
		aws.ToString(captured.Key) != "prefix/object" {
		t.Fatalf("Open() = %+v, input %+v", blob, captured)
	}
	if err := blob.Reader.Close(); err != nil || !body.closed {
		t.Fatalf("Close() = %v, closed %v", err, body.closed)
	}
}

func TestS3BlobStoreMapsOnlyNoSuchKeyAndClosesErrorBody(t *testing.T) {
	tests := []struct {
		name        string
		providerErr error
		wantMissing bool
	}{
		{name: "typed no such key", providerErr: &s3types.NoSuchKey{}, wantMissing: true},
		{name: "coded no such key", providerErr: &s3TestAPIError{code: "NoSuchKey", status: 404}, wantMissing: true},
		{name: "no such bucket", providerErr: &s3TestAPIError{code: "NoSuchBucket", status: 404}},
		{name: "generic not found", providerErr: &s3TestAPIError{code: "NotFound", status: 404}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackingReadCloser{Reader: bytes.NewReader(nil)}
			client := &fakeS3Client{get: func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
				return &s3.GetObjectOutput{Body: body}, test.providerErr
			}}
			store, err := NewS3BlobStore(client, "objects")
			if err != nil {
				t.Fatal(err)
			}
			_, openErr := store.Open(t.Context(), "object")
			if openErr == nil || errors.Is(openErr, objectstore.ErrBlobNotFound) != test.wantMissing || !body.closed {
				t.Fatalf("Open() = %v, missing %v, closed %v", openErr, errors.Is(openErr, objectstore.ErrBlobNotFound), body.closed)
			}
		})
	}
}

func TestS3BlobStoreRejectsMalformedSuccessOutput(t *testing.T) {
	tests := []struct {
		name   string
		output *s3.GetObjectOutput
		body   *trackingReadCloser
	}{
		{name: "nil output"},
		{name: "nil body", output: &s3.GetObjectOutput{ContentLength: aws.Int64(1)}},
		{name: "nil length", body: &trackingReadCloser{Reader: bytes.NewReader(nil)}},
		{name: "zero length", body: &trackingReadCloser{Reader: bytes.NewReader(nil)}},
		{name: "negative length", body: &trackingReadCloser{Reader: bytes.NewReader(nil)}},
	}
	tests[2].output = &s3.GetObjectOutput{Body: tests[2].body}
	tests[3].output = &s3.GetObjectOutput{Body: tests[3].body, ContentLength: aws.Int64(0)}
	tests[4].output = &s3.GetObjectOutput{Body: tests[4].body, ContentLength: aws.Int64(-1)}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeS3Client{get: func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
				return test.output, nil
			}}
			store, err := NewS3BlobStore(client, "objects")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Open(t.Context(), "object"); err == nil {
				t.Fatal("Open accepted malformed S3 success output")
			}
			if test.body != nil && !test.body.closed {
				t.Fatal("Open did not close the malformed S3 body")
			}
		})
	}
}

func TestS3BlobStoreValidatesInputs(t *testing.T) {
	client := &fakeS3Client{}
	if _, err := NewS3BlobStore(nil, "objects"); err == nil {
		t.Fatal("NewS3BlobStore accepted nil client")
	}
	if _, err := NewS3BlobStore(client, " objects "); err == nil {
		t.Fatal("NewS3BlobStore accepted invalid bucket")
	}
	store, err := NewS3BlobStore(client, "objects")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutIfAbsent(nil, "object", 1, bytes.NewReader([]byte{1})); err == nil {
		t.Fatal("PutIfAbsent accepted nil context")
	}
	if _, err := store.PutIfAbsent(t.Context(), "", 1, bytes.NewReader([]byte{1})); err == nil {
		t.Fatal("PutIfAbsent accepted empty key")
	}
	if _, err := store.PutIfAbsent(t.Context(), string(bytes.Repeat([]byte{'k'}, maximumS3ObjectKeyBytes+1)), 1, bytes.NewReader([]byte{1})); err == nil {
		t.Fatal("PutIfAbsent accepted oversize key")
	}
	if _, err := store.PutIfAbsent(t.Context(), "object", 0, bytes.NewReader(nil)); err == nil {
		t.Fatal("PutIfAbsent accepted zero size")
	}
	if _, err := store.PutIfAbsent(t.Context(), "object", maximumS3PutObjectBytes+1, bytes.NewReader(nil)); err == nil {
		t.Fatal("PutIfAbsent accepted an object beyond single-part S3 bounds")
	}
	if _, err := store.Open(nil, "object"); err == nil {
		t.Fatal("Open accepted nil context")
	}
	if _, err := store.Open(t.Context(), ""); err == nil {
		t.Fatal("Open accepted empty key")
	}
}

type fakeS3Client struct {
	put func(context.Context, *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	get func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error)
}

func (client *fakeS3Client) PutObject(
	ctx context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	if client.put == nil {
		return nil, errors.New("unexpected PutObject call")
	}
	return client.put(ctx, input)
}

func (client *fakeS3Client) GetObject(
	ctx context.Context,
	input *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	if client.get == nil {
		return nil, errors.New("unexpected GetObject call")
	}
	return client.get(ctx, input)
}

type s3TestAPIError struct {
	code   string
	status int
}

func (err *s3TestAPIError) Error() string                 { return err.code }
func (err *s3TestAPIError) ErrorCode() string             { return err.code }
func (err *s3TestAPIError) ErrorMessage() string          { return err.code }
func (err *s3TestAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }
func (err *s3TestAPIError) HTTPStatusCode() int           { return err.status }

type trackingReadCloser struct {
	io.Reader
	closed bool
}

type testS3HTTPClient func(*http.Request) (*http.Response, error)

func (client testS3HTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client(request)
}

func newTestS3SDKClient(t *testing.T, client testS3HTTPClient) *s3.Client {
	t.Helper()
	config := testProviderConfig()
	config.S3Endpoint = "https://s3.example.test"
	return s3.NewFromConfig(aws.Config{
		Region: config.S3Region,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test-access", SecretAccessKey: "test-secret"}, nil
		}),
		HTTPClient: client,
	}, s3ClientOptions(config))
}

func s3SDKResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type":     []string{"application/xml"},
			"X-Amz-Request-Id": []string{"test-request"},
		},
		Body: io.NopCloser(bytes.NewBufferString(body)), Request: request,
	}
}

func s3ErrorXML(code string) string {
	return "<Error><Code>" + code + "</Code><Message>test failure</Message><RequestId>test-request</RequestId></Error>"
}

func (reader *trackingReadCloser) Close() error {
	reader.closed = true
	return nil
}
