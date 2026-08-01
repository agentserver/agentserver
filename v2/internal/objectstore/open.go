package objectstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

func (store *Store) Open(ctx context.Context, scope Scope) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, errors.New("encrypted object read context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil || store.backend == nil || store.keys == nil {
		return nil, errors.New("encrypted object store is not initialized")
	}
	if err := validateScope(scope, store.maximum); err != nil {
		return nil, err
	}
	blob, err := store.backend.Open(ctx, store.objectKey(scope))
	if err != nil {
		return nil, fmt.Errorf("open immutable encrypted blob: %w", err)
	}
	if blob.Reader == nil {
		return nil, errors.New("immutable blob backend returned a nil reader")
	}
	fail := func(cause error) (io.ReadCloser, error) {
		return nil, errors.Join(cause, blob.Reader.Close())
	}
	header, err := parseHeader(blob.Reader)
	if err != nil {
		return fail(err)
	}
	if !header.matches(scope) {
		return fail(errObjectAuthorityMismatch)
	}
	wantCiphertextSize, err := encryptedObjectSize(scope.Descriptor.Size, int64(len(header.Raw)))
	if err != nil || blob.Size != wantCiphertextSize {
		return fail(errors.New("encrypted object ciphertext size does not match its framing"))
	}
	key, err := store.keys.DecryptDataKey(ctx, header.KeyID, header.WrappedKey, scopeAuthority(scope))
	if err != nil {
		return fail(fmt.Errorf("decrypt encrypted object data key: %w", err))
	}
	defer clear(key[:])
	var zero [32]byte
	if subtle.ConstantTimeCompare(key[:], zero[:]) == 1 {
		return fail(errors.New("KMS returned an invalid all-zero decrypted data key"))
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return fail(fmt.Errorf("initialize encrypted object AES: %w", err))
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || aead.NonceSize() != objectGCMNonceBytes || aead.Overhead() != objectGCMTagBytes {
		return fail(errors.New("initialize encrypted object AES-GCM profile"))
	}
	return &openedObjectReader{
		ctx: ctx, source: blob.Reader, scope: scope, aead: aead,
		header: header.Raw, noncePrefix: header.NoncePrefix,
		hasher: sha256.New(), remaining: scope.Descriptor.Size,
	}, nil
}

type openedObjectReader struct {
	ctx         context.Context
	source      io.ReadCloser
	scope       Scope
	aead        cipher.AEAD
	header      []byte
	noncePrefix [objectNoncePrefixBytes]byte
	hasher      hash.Hash
	remaining   int64
	index       uint32
	pending     []byte
	pendingAt   int
	verified    bool
	terminalErr error
	closed      bool
}

func (reader *openedObjectReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if reader.closed {
		return 0, errors.New("encrypted object reader is closed")
	}
	if reader.terminalErr != nil {
		return 0, reader.terminalErr
	}
	for {
		if reader.pendingAt < len(reader.pending) {
			written := copy(destination, reader.pending[reader.pendingAt:])
			reader.pendingAt += written
			if reader.pendingAt == len(reader.pending) {
				clear(reader.pending)
				reader.pending = nil
				reader.pendingAt = 0
			}
			return written, nil
		}
		if reader.verified {
			return 0, io.EOF
		}
		if err := reader.loadChunk(); err != nil {
			reader.terminalErr = err
			return 0, err
		}
	}
}

func (reader *openedObjectReader) loadChunk() error {
	if err := reader.ctx.Err(); err != nil {
		return err
	}
	if reader.remaining < 1 {
		return errors.New("encrypted object reader reached an invalid plaintext cursor")
	}
	plaintextLength := int64(objectChunkBytes)
	if reader.remaining < plaintextLength {
		plaintextLength = reader.remaining
	}
	var frameLength [4]byte
	if _, err := io.ReadFull(reader.source, frameLength[:]); err != nil {
		return fmt.Errorf("read encrypted object chunk length: %w", err)
	}
	if binary.BigEndian.Uint32(frameLength[:]) != uint32(plaintextLength) {
		return errors.New("encrypted object chunk length does not match the plaintext cursor")
	}
	ciphertext := make([]byte, int(plaintextLength)+reader.aead.Overhead())
	if _, err := io.ReadFull(reader.source, ciphertext); err != nil {
		clear(ciphertext)
		return fmt.Errorf("read encrypted object chunk ciphertext: %w", err)
	}
	nonce := objectNonce(reader.noncePrefix, reader.index)
	aad := objectChunkAAD(reader.header, reader.index, uint32(plaintextLength))
	plaintext, err := reader.aead.Open(nil, nonce[:], ciphertext, aad)
	clear(ciphertext)
	if err != nil {
		return errors.New("encrypted object chunk failed authentication")
	}
	_, _ = reader.hasher.Write(plaintext)
	reader.remaining -= plaintextLength
	reader.index++
	if reader.remaining == 0 {
		var extra [1]byte
		extraBytes, extraErr := reader.source.Read(extra[:])
		if extraBytes != 0 || !errors.Is(extraErr, io.EOF) {
			clear(plaintext)
			return errors.New("encrypted object contains trailing ciphertext")
		}
		if subtle.ConstantTimeCompare(reader.hasher.Sum(nil), reader.scope.Descriptor.SHA256[:]) != 1 {
			clear(plaintext)
			return errors.New("decrypted object does not match its committed digest")
		}
		reader.verified = true
	}
	reader.pending = plaintext
	return nil
}

func (reader *openedObjectReader) Close() error {
	if reader == nil || reader.closed {
		return nil
	}
	var verifyErr error
	if reader.terminalErr != nil {
		verifyErr = reader.terminalErr
	} else if !reader.verified || len(reader.pending) != 0 {
		_, verifyErr = io.Copy(io.Discard, reader)
	}
	reader.closed = true
	clear(reader.pending)
	reader.pending = nil
	clear(reader.header)
	reader.header = nil
	return errors.Join(verifyErr, reader.source.Close())
}
