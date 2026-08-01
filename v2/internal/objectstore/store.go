// Package objectstore implements the application-owned encrypted object
// protocol used above an immutable S3-compatible blob backend. Concrete KMS
// and S3 clients implement the narrow interfaces in this package; plaintext
// object authority never depends on provider metadata or presigned URLs.
package objectstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	KindUserPrompt Kind = "user-prompt"
	KindCheckpoint Kind = "checkpoint"

	defaultObjectPrefix      = "agentserver-v2/objects"
	maximumObjectPrefixBytes = 256
	maximumMediaTypeBytes    = 255
	maximumKeyIDBytes        = 256
	maximumWrappedKeyBytes   = 16 * 1024
	maximumSupportedBytes    = int64(1 << 40)
)

var (
	ErrObjectConflict          = errors.New("immutable encrypted object conflicts with requested authority")
	ErrBlobNotFound            = errors.New("immutable blob does not exist")
	errObjectAuthorityMismatch = errors.New("encrypted object header does not match requested authority")

	canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	prefixSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type Kind string

// Descriptor is the complete plaintext authority committed by Core. SHA256,
// Size and MediaType describe plaintext, never ciphertext.
type Descriptor struct {
	ObjectID  string
	SHA256    [sha256.Size]byte
	Size      int64
	MediaType string
}

// Scope prevents a valid encrypted object from being substituted across a
// workspace or semantic object class even when a database pointer is wrong.
type Scope struct {
	WorkspaceID string
	Kind        Kind
	Descriptor  Descriptor
}

// GeneratedDataKey is one fresh, independently generated KMS AES-256 data key.
// WrappedKey is safe to persist in the encrypted object header; Plaintext is
// cleared by Store after the AEAD has been initialized.
type GeneratedDataKey struct {
	KeyID      string
	Plaintext  [32]byte
	WrappedKey []byte
}

// DataKeyProvider is the provider-neutral KMS envelope boundary. GenerateDataKey
// must return a fresh key for every successful call. Both calls must
// cryptographically bind aad as encryption context.
type DataKeyProvider interface {
	GenerateDataKey(context.Context, []byte) (GeneratedDataKey, error)
	DecryptDataKey(context.Context, string, []byte, []byte) ([32]byte, error)
}

type Blob struct {
	Reader io.ReadCloser
	Size   int64
}

type PutResult struct {
	Created bool
}

// ImmutableBlobStore is the S3-compatible persistence boundary. PutIfAbsent
// must atomically publish only after consuming exactly size bytes from source;
// partial uploads are never visible. Returning Created=false means an object
// already exists at key. Ambiguous provider errors are returned as errors and
// reconciled by an exact retry through Store.Put.
type ImmutableBlobStore interface {
	PutIfAbsent(context.Context, string, int64, io.Reader) (PutResult, error)
	Open(context.Context, string) (Blob, error)
}

type Config struct {
	Backend               ImmutableBlobStore
	Keys                  DataKeyProvider
	Prefix                string
	MaximumPlaintextBytes int64
	Random                io.Reader
}

type Store struct {
	backend ImmutableBlobStore
	keys    DataKeyProvider
	prefix  string
	maximum int64
	random  io.Reader
}

func New(config Config) (*Store, error) {
	if config.Backend == nil || config.Keys == nil {
		return nil, errors.New("encrypted object backend and data-key provider are required")
	}
	if config.Prefix == "" {
		config.Prefix = defaultObjectPrefix
	}
	if err := validatePrefix(config.Prefix); err != nil {
		return nil, err
	}
	if config.MaximumPlaintextBytes < 1 || config.MaximumPlaintextBytes > maximumSupportedBytes {
		return nil, fmt.Errorf("encrypted object plaintext bound must be between 1 and %d bytes", maximumSupportedBytes)
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Store{
		backend: config.Backend, keys: config.Keys, prefix: config.Prefix,
		maximum: config.MaximumPlaintextBytes, random: config.Random,
	}, nil
}

// Put encrypts and atomically creates one immutable object. Exact retries
// verify the already-persisted ciphertext through KMS and the complete
// plaintext digest before succeeding.
func (store *Store) Put(ctx context.Context, scope Scope, source io.Reader) error {
	if ctx == nil {
		return errors.New("encrypted object write context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || store.backend == nil || store.keys == nil || store.random == nil {
		return errors.New("encrypted object store is not initialized")
	}
	if source == nil {
		return errors.New("encrypted object plaintext source is required")
	}
	if err := validateScope(scope, store.maximum); err != nil {
		return err
	}
	authority := scopeAuthority(scope)
	dataKey, err := store.keys.GenerateDataKey(ctx, authority)
	if err != nil {
		return fmt.Errorf("generate encrypted object data key: %w", err)
	}
	defer clear(dataKey.Plaintext[:])
	if err := validateGeneratedDataKey(dataKey); err != nil {
		return err
	}
	var noncePrefix [objectNoncePrefixBytes]byte
	if _, err := io.ReadFull(store.random, noncePrefix[:]); err != nil {
		return fmt.Errorf("generate encrypted object nonce: %w", err)
	}
	header, err := marshalHeader(scope, dataKey.KeyID, dataKey.WrappedKey, noncePrefix)
	if err != nil {
		return err
	}
	sealed, err := newSealedObjectReader(ctx, source, scope, dataKey.Plaintext[:], header, noncePrefix)
	if err != nil {
		return err
	}
	ciphertextSize, err := encryptedObjectSize(scope.Descriptor.Size, int64(len(header)))
	if err != nil {
		return err
	}
	result, err := store.backend.PutIfAbsent(ctx, store.objectKey(scope), ciphertextSize, sealed)
	if err != nil {
		return fmt.Errorf("put immutable encrypted object: %w", err)
	}
	if result.Created {
		if !sealed.Complete() {
			return errors.New("immutable blob backend committed without consuming and verifying the complete encrypted object")
		}
		return nil
	}
	if err := store.verifyExisting(ctx, scope); err != nil {
		if errors.Is(err, errObjectAuthorityMismatch) {
			return errors.Join(ErrObjectConflict, err)
		}
		return fmt.Errorf("verify existing immutable encrypted object: %w", err)
	}
	return nil
}

func (store *Store) verifyExisting(ctx context.Context, scope Scope) error {
	reader, err := store.Open(ctx, scope)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, reader)
	closeErr := reader.Close()
	return errors.Join(readErr, closeErr)
}

func (store *Store) objectKey(scope Scope) string {
	return store.prefix + "/" + scope.WorkspaceID + "/" + string(scope.Kind) + "/" + scope.Descriptor.ObjectID
}

func validateScope(scope Scope, maximum int64) error {
	if !canonicalUUID(scope.WorkspaceID) {
		return errors.New("encrypted object workspace must be a non-zero canonical UUID")
	}
	if scope.Kind != KindUserPrompt && scope.Kind != KindCheckpoint {
		return errors.New("encrypted object kind is unsupported")
	}
	if !canonicalUUID(scope.Descriptor.ObjectID) {
		return errors.New("encrypted object identity must be a non-zero canonical UUID")
	}
	if scope.Descriptor.Size < 1 || scope.Descriptor.Size > maximum {
		return errors.New("encrypted object plaintext size is outside the configured bound")
	}
	if !validMediaType(scope.Descriptor.MediaType) {
		return errors.New("encrypted object media type is invalid or outside protocol bounds")
	}
	return nil
}

func validateGeneratedDataKey(key GeneratedDataKey) error {
	if !validBoundedText(key.KeyID, maximumKeyIDBytes) || len(key.WrappedKey) < 1 || len(key.WrappedKey) > maximumWrappedKeyBytes {
		return errors.New("KMS data-key envelope is outside encrypted object protocol bounds")
	}
	var zero [32]byte
	if subtle.ConstantTimeCompare(key.Plaintext[:], zero[:]) == 1 {
		return errors.New("KMS returned an invalid all-zero plaintext data key")
	}
	return nil
}

func scopeAuthority(scope Scope) []byte {
	result := make([]byte, 0, 256)
	result = append(result, []byte("agentserver-v2/encrypted-object/authority-v1\x00")...)
	for _, value := range []string{scope.WorkspaceID, string(scope.Kind), scope.Descriptor.ObjectID, scope.Descriptor.MediaType} {
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(value)))
		result = append(result, length[:]...)
		result = append(result, value...)
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(scope.Descriptor.Size))
	result = append(result, size[:]...)
	result = append(result, scope.Descriptor.SHA256[:]...)
	return result
}

func validatePrefix(prefix string) error {
	if len(prefix) < 1 || len(prefix) > maximumObjectPrefixBytes || strings.Trim(prefix, "/") != prefix {
		return errors.New("encrypted object key prefix is empty, padded, or too large")
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "." || segment == ".." || !prefixSegmentPattern.MatchString(segment) {
			return errors.New("encrypted object key prefix contains an invalid segment")
		}
	}
	return nil
}

func canonicalUUID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && canonicalUUIDPattern.MatchString(value)
}

func validMediaType(value string) bool {
	if !validBoundedText(value, maximumMediaTypeBytes) || strings.TrimSpace(value) != value {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	return err == nil && mediaType != "" && mime.FormatMediaType(mediaType, parameters) == value
}

func validBoundedText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
