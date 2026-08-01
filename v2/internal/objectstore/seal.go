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

type sealedObjectReader struct {
	ctx         context.Context
	source      io.Reader
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
}

func newSealedObjectReader(
	ctx context.Context,
	source io.Reader,
	scope Scope,
	key, header []byte,
	noncePrefix [objectNoncePrefixBytes]byte,
) (*sealedObjectReader, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize encrypted object AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || aead.NonceSize() != objectGCMNonceBytes || aead.Overhead() != objectGCMTagBytes {
		return nil, errors.New("initialize encrypted object AES-GCM profile")
	}
	return &sealedObjectReader{
		ctx: ctx, source: source, scope: scope, aead: aead,
		header: append([]byte(nil), header...), noncePrefix: noncePrefix,
		hasher: sha256.New(), remaining: scope.Descriptor.Size,
		pending: append([]byte(nil), header...),
	}, nil
}

func (reader *sealedObjectReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
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

func (reader *sealedObjectReader) loadChunk() error {
	if err := reader.ctx.Err(); err != nil {
		return err
	}
	if reader.remaining < 1 {
		return errors.New("encrypted object sealer reached an invalid plaintext cursor")
	}
	plaintextLength := int64(objectChunkBytes)
	if reader.remaining < plaintextLength {
		plaintextLength = reader.remaining
	}
	plaintext := make([]byte, int(plaintextLength))
	if _, err := io.ReadFull(reader.source, plaintext); err != nil {
		clear(plaintext)
		return fmt.Errorf("read encrypted object plaintext chunk: %w", err)
	}
	_, _ = reader.hasher.Write(plaintext)
	reader.remaining -= plaintextLength
	if reader.remaining == 0 {
		var extra [1]byte
		extraBytes, extraErr := reader.source.Read(extra[:])
		if extraBytes != 0 || !errors.Is(extraErr, io.EOF) {
			clear(plaintext)
			return errors.New("encrypted object plaintext exceeds its declared size")
		}
		if subtle.ConstantTimeCompare(reader.hasher.Sum(nil), reader.scope.Descriptor.SHA256[:]) != 1 {
			clear(plaintext)
			return errors.New("encrypted object plaintext does not match its declared digest")
		}
	}
	nonce := objectNonce(reader.noncePrefix, reader.index)
	aad := objectChunkAAD(reader.header, reader.index, uint32(plaintextLength))
	ciphertext := reader.aead.Seal(nil, nonce[:], plaintext, aad)
	clear(plaintext)
	frame := make([]byte, 4+len(ciphertext))
	binary.BigEndian.PutUint32(frame[:4], uint32(plaintextLength))
	copy(frame[4:], ciphertext)
	clear(ciphertext)
	reader.pending = frame
	reader.index++
	if reader.remaining == 0 {
		reader.verified = true
	}
	return nil
}

func (reader *sealedObjectReader) Complete() bool {
	return reader != nil && reader.verified && reader.remaining == 0 && len(reader.pending) == 0 && reader.terminalErr == nil
}
