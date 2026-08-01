package objectstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	objectChunkBytes       = 1024 * 1024
	objectNoncePrefixBytes = 8
	objectGCMNonceBytes    = 12
	objectGCMTagBytes      = 16
	headerPrefixBytes      = 20
	headerFixedBodyBytes   = 133
	maximumHeaderBodyBytes = headerFixedBodyBytes + maximumMediaTypeBytes + maximumKeyIDBytes + maximumWrappedKeyBytes
)

var objectMagic = [16]byte{'A', 'S', 'V', '2', 'O', 'B', 'J', 'E', 'C', 'T', 0, 0, 0, 0, 0, 1}

type objectHeader struct {
	Scope       Scope
	KeyID       string
	WrappedKey  []byte
	NoncePrefix [objectNoncePrefixBytes]byte
	Raw         []byte
}

func marshalHeader(scope Scope, keyID string, wrappedKey []byte, noncePrefix [objectNoncePrefixBytes]byte) ([]byte, error) {
	if !validBoundedText(keyID, maximumKeyIDBytes) || len(wrappedKey) < 1 || len(wrappedKey) > maximumWrappedKeyBytes {
		return nil, errors.New("encrypted object key envelope cannot be represented in the v1 header")
	}
	bodyLength := headerFixedBodyBytes + len(scope.Descriptor.MediaType) + len(keyID) + len(wrappedKey)
	if bodyLength > maximumHeaderBodyBytes {
		return nil, errors.New("encrypted object header exceeds its size bound")
	}
	result := make([]byte, headerPrefixBytes+bodyLength)
	copy(result[:16], objectMagic[:])
	binary.BigEndian.PutUint32(result[16:20], uint32(bodyLength))
	body := result[headerPrefixBytes:]
	binary.BigEndian.PutUint32(body[0:4], objectChunkBytes)
	binary.BigEndian.PutUint64(body[4:12], uint64(scope.Descriptor.Size))
	copy(body[12:44], scope.Descriptor.SHA256[:])
	copy(body[44:52], noncePrefix[:])
	body[52] = encodeObjectKind(scope.Kind)
	copy(body[53:89], scope.WorkspaceID)
	copy(body[89:125], scope.Descriptor.ObjectID)
	binary.BigEndian.PutUint16(body[125:127], uint16(len(scope.Descriptor.MediaType)))
	binary.BigEndian.PutUint16(body[127:129], uint16(len(keyID)))
	binary.BigEndian.PutUint32(body[129:133], uint32(len(wrappedKey)))
	offset := headerFixedBodyBytes
	copy(body[offset:], scope.Descriptor.MediaType)
	offset += len(scope.Descriptor.MediaType)
	copy(body[offset:], keyID)
	offset += len(keyID)
	copy(body[offset:], wrappedKey)
	return result, nil
}

func parseHeader(source io.Reader) (objectHeader, error) {
	var prefix [headerPrefixBytes]byte
	if _, err := io.ReadFull(source, prefix[:]); err != nil {
		return objectHeader{}, fmt.Errorf("read encrypted object header prefix: %w", err)
	}
	if !bytes.Equal(prefix[:16], objectMagic[:]) {
		return objectHeader{}, errors.New("encrypted object magic or version is invalid")
	}
	bodyLength := int(binary.BigEndian.Uint32(prefix[16:20]))
	if bodyLength < headerFixedBodyBytes || bodyLength > maximumHeaderBodyBytes {
		return objectHeader{}, errors.New("encrypted object header length is outside protocol bounds")
	}
	body := make([]byte, bodyLength)
	if _, err := io.ReadFull(source, body); err != nil {
		return objectHeader{}, fmt.Errorf("read encrypted object header body: %w", err)
	}
	if binary.BigEndian.Uint32(body[0:4]) != objectChunkBytes {
		return objectHeader{}, errors.New("encrypted object chunk profile is unsupported")
	}
	kind, err := decodeObjectKind(body[52])
	if err != nil {
		return objectHeader{}, err
	}
	mediaLength := int(binary.BigEndian.Uint16(body[125:127]))
	keyIDLength := int(binary.BigEndian.Uint16(body[127:129]))
	wrappedLength := int(binary.BigEndian.Uint32(body[129:133]))
	if mediaLength < 1 || mediaLength > maximumMediaTypeBytes || keyIDLength < 1 || keyIDLength > maximumKeyIDBytes ||
		wrappedLength < 1 || wrappedLength > maximumWrappedKeyBytes ||
		headerFixedBodyBytes+mediaLength+keyIDLength+wrappedLength != len(body) {
		return objectHeader{}, errors.New("encrypted object variable header fields are outside protocol bounds")
	}
	offset := headerFixedBodyBytes
	mediaType := string(body[offset : offset+mediaLength])
	offset += mediaLength
	keyID := string(body[offset : offset+keyIDLength])
	offset += keyIDLength
	wrappedKey := append([]byte(nil), body[offset:offset+wrappedLength]...)
	var digest [sha256.Size]byte
	copy(digest[:], body[12:44])
	var noncePrefix [objectNoncePrefixBytes]byte
	copy(noncePrefix[:], body[44:52])
	header := objectHeader{
		Scope: Scope{
			WorkspaceID: string(body[53:89]), Kind: kind,
			Descriptor: Descriptor{
				ObjectID: string(body[89:125]), SHA256: digest,
				Size: int64(binary.BigEndian.Uint64(body[4:12])), MediaType: mediaType,
			},
		},
		KeyID: keyID, WrappedKey: wrappedKey, NoncePrefix: noncePrefix,
		Raw: append(append([]byte(nil), prefix[:]...), body...),
	}
	if err := validateGeneratedHeader(header); err != nil {
		return objectHeader{}, err
	}
	return header, nil
}

func validateGeneratedHeader(header objectHeader) error {
	if err := validateScope(header.Scope, maximumSupportedBytes); err != nil {
		return fmt.Errorf("validate encrypted object header authority: %w", err)
	}
	if !validBoundedText(header.KeyID, maximumKeyIDBytes) || len(header.WrappedKey) < 1 || len(header.WrappedKey) > maximumWrappedKeyBytes {
		return errors.New("encrypted object header key envelope is invalid")
	}
	return nil
}

func (header objectHeader) matches(scope Scope) bool {
	return header.Scope.WorkspaceID == scope.WorkspaceID && header.Scope.Kind == scope.Kind &&
		header.Scope.Descriptor == scope.Descriptor
}

func encryptedObjectSize(plaintextSize, headerSize int64) (int64, error) {
	if plaintextSize < 1 || plaintextSize > maximumSupportedBytes || headerSize < headerPrefixBytes || headerSize > headerPrefixBytes+maximumHeaderBodyBytes {
		return 0, errors.New("encrypted object dimensions are outside protocol bounds")
	}
	chunks := (plaintextSize + objectChunkBytes - 1) / objectChunkBytes
	if chunks < 1 || chunks > math.MaxUint32 {
		return 0, errors.New("encrypted object chunk count is outside protocol bounds")
	}
	overhead := chunks * (4 + objectGCMTagBytes)
	if plaintextSize > math.MaxInt64-headerSize-overhead {
		return 0, errors.New("encrypted object ciphertext size overflows int64")
	}
	return headerSize + plaintextSize + overhead, nil
}

func encodeObjectKind(kind Kind) byte {
	switch kind {
	case KindUserPrompt:
		return 1
	case KindCheckpoint:
		return 2
	default:
		return 0
	}
}

func decodeObjectKind(encoded byte) (Kind, error) {
	switch encoded {
	case 1:
		return KindUserPrompt, nil
	case 2:
		return KindCheckpoint, nil
	default:
		return "", errors.New("encrypted object kind discriminator is unsupported")
	}
}

func objectNonce(prefix [objectNoncePrefixBytes]byte, index uint32) [objectGCMNonceBytes]byte {
	var nonce [objectGCMNonceBytes]byte
	copy(nonce[:objectNoncePrefixBytes], prefix[:])
	binary.BigEndian.PutUint32(nonce[objectNoncePrefixBytes:], index)
	return nonce
}

func objectChunkAAD(header []byte, index, plaintextLength uint32) []byte {
	headerDigest := sha256.Sum256(header)
	result := make([]byte, 0, 96)
	result = append(result, []byte("agentserver-v2/encrypted-object/aes-256-gcm-chunk-v1\x00")...)
	result = append(result, headerDigest[:]...)
	var cursor [8]byte
	binary.BigEndian.PutUint32(cursor[0:4], index)
	binary.BigEndian.PutUint32(cursor[4:8], plaintextLength)
	return append(result, cursor[:]...)
}
