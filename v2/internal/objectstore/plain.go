package objectstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
	"io"
)

// PlainConfig selects the explicitly plaintext immutable object profile. S3
// receives the exact prompt/checkpoint bytes described by Scope.Descriptor.
type PlainConfig struct {
	Backend               ImmutableBlobStore
	Prefix                string
	MaximumPlaintextBytes int64
}

// PlainStore preserves pointer, size and digest authority without encrypting
// provider bytes. The bucket is therefore inside the trusted data boundary.
type PlainStore struct {
	backend ImmutableBlobStore
	prefix  string
	maximum int64
}

func NewPlain(config PlainConfig) (*PlainStore, error) {
	if config.Backend == nil {
		return nil, errors.New("plaintext object backend is required")
	}
	if config.Prefix == "" {
		config.Prefix = defaultObjectPrefix
	}
	if err := validatePrefix(config.Prefix); err != nil {
		return nil, err
	}
	if config.MaximumPlaintextBytes < 1 || config.MaximumPlaintextBytes > maximumSupportedBytes {
		return nil, fmt.Errorf("plaintext object bound must be between 1 and %d bytes", maximumSupportedBytes)
	}
	return &PlainStore{backend: config.Backend, prefix: config.Prefix, maximum: config.MaximumPlaintextBytes}, nil
}

func (store *PlainStore) Put(ctx context.Context, scope Scope, source io.Reader) error {
	if ctx == nil {
		return errors.New("plaintext object write context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || store.backend == nil {
		return errors.New("plaintext object store is not initialized")
	}
	if source == nil {
		return errors.New("plaintext object source is required")
	}
	if err := validateScope(scope, store.maximum); err != nil {
		return err
	}
	verified := &plainSourceReader{
		ctx: ctx, source: source, expected: scope.Descriptor.SHA256,
		hasher: sha256.New(), remaining: scope.Descriptor.Size,
	}
	result, err := store.backend.PutIfAbsent(ctx, store.objectKey(scope), scope.Descriptor.Size, verified)
	if err != nil {
		return fmt.Errorf("put immutable plaintext object: %w", err)
	}
	if result.Created {
		if !verified.Complete() {
			return errors.New("immutable blob backend committed without consuming and verifying the complete plaintext object")
		}
		return nil
	}
	if err := store.verifyExisting(ctx, scope); err != nil {
		if errors.Is(err, errObjectAuthorityMismatch) {
			return errors.Join(ErrObjectConflict, err)
		}
		return fmt.Errorf("verify existing immutable plaintext object: %w", err)
	}
	return nil
}

func (store *PlainStore) Open(ctx context.Context, scope Scope) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, errors.New("plaintext object read context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil || store.backend == nil {
		return nil, errors.New("plaintext object store is not initialized")
	}
	if err := validateScope(scope, store.maximum); err != nil {
		return nil, err
	}
	blob, err := store.backend.Open(ctx, store.objectKey(scope))
	if err != nil {
		return nil, fmt.Errorf("open immutable plaintext blob: %w", err)
	}
	if blob.Reader == nil {
		return nil, errors.New("immutable blob backend returned a nil reader")
	}
	if blob.Size != scope.Descriptor.Size {
		return nil, errors.Join(errObjectAuthorityMismatch, blob.Reader.Close())
	}
	return &plainObjectReader{
		ctx: ctx, source: blob.Reader, expected: scope.Descriptor.SHA256,
		hasher: sha256.New(), remaining: scope.Descriptor.Size,
	}, nil
}

func (store *PlainStore) verifyExisting(ctx context.Context, scope Scope) error {
	reader, err := store.Open(ctx, scope)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(errObjectAuthorityMismatch, readErr, closeErr)
	}
	return nil
}

func (store *PlainStore) objectKey(scope Scope) string {
	return store.prefix + "/" + scope.WorkspaceID + "/" + string(scope.Kind) + "/" + scope.Descriptor.ObjectID
}

type plainSourceReader struct {
	ctx       context.Context
	source    io.Reader
	expected  [sha256.Size]byte
	hasher    hash.Hash
	remaining int64
	complete  bool
	terminal  error
}

func (reader *plainSourceReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if reader.terminal != nil {
		return 0, reader.terminal
	}
	if reader.complete {
		return 0, io.EOF
	}
	if err := reader.ctx.Err(); err != nil {
		reader.terminal = err
		return 0, err
	}
	if int64(len(destination)) > reader.remaining {
		destination = destination[:reader.remaining]
	}
	written, readErr := reader.source.Read(destination)
	if written < 0 || int64(written) > reader.remaining {
		reader.terminal = errors.New("plaintext object source violated io.Reader bounds")
		return 0, reader.terminal
	}
	if written > 0 {
		_, _ = reader.hasher.Write(destination[:written])
		reader.remaining -= int64(written)
	}
	if reader.remaining == 0 {
		var extra [1]byte
		extraBytes, extraErr := reader.source.Read(extra[:])
		if extraBytes != 0 || !errors.Is(extraErr, io.EOF) {
			reader.terminal = errors.New("plaintext object exceeds its declared size")
			return written, reader.terminal
		}
		if subtle.ConstantTimeCompare(reader.hasher.Sum(nil), reader.expected[:]) != 1 {
			reader.terminal = errors.New("plaintext object does not match its declared digest")
			return written, reader.terminal
		}
		reader.complete = true
		return written, nil
	}
	if readErr != nil {
		reader.terminal = fmt.Errorf("read plaintext object source: %w", readErr)
		return written, reader.terminal
	}
	if written == 0 {
		reader.terminal = io.ErrNoProgress
		return 0, reader.terminal
	}
	return written, nil
}

func (reader *plainSourceReader) Complete() bool {
	return reader != nil && reader.complete && reader.remaining == 0 && reader.terminal == nil
}

type plainObjectReader struct {
	ctx       context.Context
	source    io.ReadCloser
	expected  [sha256.Size]byte
	hasher    hash.Hash
	remaining int64
	verified  bool
	terminal  error
	closed    bool
}

func (reader *plainObjectReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if reader.closed {
		return 0, errors.New("plaintext object reader is closed")
	}
	if reader.terminal != nil {
		return 0, reader.terminal
	}
	if reader.verified {
		return 0, io.EOF
	}
	if err := reader.ctx.Err(); err != nil {
		reader.terminal = err
		return 0, err
	}
	if int64(len(destination)) > reader.remaining {
		destination = destination[:reader.remaining]
	}
	written, readErr := reader.source.Read(destination)
	if written < 0 || int64(written) > reader.remaining {
		reader.terminal = errors.New("plaintext blob violated io.Reader bounds")
		return 0, reader.terminal
	}
	if written > 0 {
		_, _ = reader.hasher.Write(destination[:written])
		reader.remaining -= int64(written)
	}
	if reader.remaining == 0 {
		var extra [1]byte
		extraBytes, extraErr := reader.source.Read(extra[:])
		if extraBytes != 0 || !errors.Is(extraErr, io.EOF) || subtle.ConstantTimeCompare(reader.hasher.Sum(nil), reader.expected[:]) != 1 {
			reader.terminal = errObjectAuthorityMismatch
			return written, reader.terminal
		}
		reader.verified = true
		return written, nil
	}
	if readErr != nil {
		reader.terminal = errors.Join(errObjectAuthorityMismatch, readErr)
		return written, reader.terminal
	}
	if written == 0 {
		reader.terminal = io.ErrNoProgress
		return 0, reader.terminal
	}
	return written, nil
}

func (reader *plainObjectReader) Close() error {
	if reader == nil || reader.closed {
		return nil
	}
	var verifyErr error
	if reader.terminal != nil {
		verifyErr = reader.terminal
	} else if !reader.verified {
		_, verifyErr = io.Copy(io.Discard, reader)
	}
	reader.closed = true
	return errors.Join(verifyErr, reader.source.Close())
}

var _ Protocol = (*PlainStore)(nil)
