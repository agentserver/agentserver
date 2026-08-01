package awsprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

const (
	kmsContextProtocolKey   = "agentserver-protocol"
	kmsContextProtocolValue = "encrypted-object-v1"
	kmsContextAuthorityKey  = "agentserver-authority-sha256"
	maximumAuthorityBytes   = 4 * 1024
	maximumWrappedKeyBytes  = 16 * 1024
	maximumKMSKeyIDBytes    = 256
)

type kmsAPI interface {
	GenerateDataKey(context.Context, *kms.GenerateDataKeyInput, ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error)
	Decrypt(context.Context, *kms.DecryptInput, ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// KMSDataKeyProvider binds every envelope operation to a domain-separated hash
// of the complete application authority. Raw workspace/object authority is not
// copied into KMS logs through EncryptionContext.
type KMSDataKeyProvider struct {
	client kmsAPI
	keyID  string
}

func NewKMSDataKeyProvider(client kmsAPI, keyID string) (*KMSDataKeyProvider, error) {
	if client == nil {
		return nil, errors.New("KMS client is required")
	}
	if !validProviderText(keyID, maximumKMSKeyIDBytes) {
		return nil, errors.New("KMS key ID is required and must be bounded printable text")
	}
	return &KMSDataKeyProvider{client: client, keyID: keyID}, nil
}

func (provider *KMSDataKeyProvider) GenerateDataKey(
	ctx context.Context,
	authority []byte,
) (objectstore.GeneratedDataKey, error) {
	if ctx == nil {
		return objectstore.GeneratedDataKey{}, errors.New("KMS generate-data-key context is required")
	}
	if err := ctx.Err(); err != nil {
		return objectstore.GeneratedDataKey{}, err
	}
	if provider == nil || provider.client == nil || provider.keyID == "" {
		return objectstore.GeneratedDataKey{}, errors.New("KMS data-key provider is not initialized")
	}
	if len(authority) < 1 || len(authority) > maximumAuthorityBytes {
		return objectstore.GeneratedDataKey{}, errors.New("KMS data-key authority is outside its size bound")
	}
	output, err := provider.client.GenerateDataKey(ctx, &kms.GenerateDataKeyInput{
		KeyId:             aws.String(provider.keyID),
		KeySpec:           kmstypes.DataKeySpecAes256,
		EncryptionContext: kmsEncryptionContext(authority),
	})
	if output != nil {
		defer clear(output.Plaintext)
	}
	if err != nil {
		return objectstore.GeneratedDataKey{}, fmt.Errorf("KMS generate AES-256 data key: %w", err)
	}
	if output == nil || output.KeyId == nil || !validProviderText(*output.KeyId, maximumKMSKeyIDBytes) {
		return objectstore.GeneratedDataKey{}, errors.New("KMS generate-data-key returned an invalid key ID")
	}
	if len(output.Plaintext) != 32 {
		return objectstore.GeneratedDataKey{}, errors.New("KMS generate-data-key did not return exactly 32 plaintext bytes")
	}
	if len(output.CiphertextBlob) < 1 || len(output.CiphertextBlob) > maximumWrappedKeyBytes {
		return objectstore.GeneratedDataKey{}, errors.New("KMS generate-data-key returned an invalid wrapped key")
	}
	var plaintext [32]byte
	copy(plaintext[:], output.Plaintext)
	return objectstore.GeneratedDataKey{
		KeyID:      *output.KeyId,
		Plaintext:  plaintext,
		WrappedKey: append([]byte(nil), output.CiphertextBlob...),
	}, nil
}

func (provider *KMSDataKeyProvider) DecryptDataKey(
	ctx context.Context,
	keyID string,
	wrappedKey []byte,
	authority []byte,
) ([32]byte, error) {
	if ctx == nil {
		return [32]byte{}, errors.New("KMS decrypt-data-key context is required")
	}
	if err := ctx.Err(); err != nil {
		return [32]byte{}, err
	}
	if provider == nil || provider.client == nil || provider.keyID == "" {
		return [32]byte{}, errors.New("KMS data-key provider is not initialized")
	}
	if !validProviderText(keyID, maximumKMSKeyIDBytes) {
		return [32]byte{}, errors.New("KMS decrypt-data-key stored key ID is invalid")
	}
	if len(wrappedKey) < 1 || len(wrappedKey) > maximumWrappedKeyBytes {
		return [32]byte{}, errors.New("KMS decrypt-data-key wrapped key is outside its size bound")
	}
	if len(authority) < 1 || len(authority) > maximumAuthorityBytes {
		return [32]byte{}, errors.New("KMS data-key authority is outside its size bound")
	}
	output, err := provider.client.Decrypt(ctx, &kms.DecryptInput{
		KeyId:               aws.String(keyID),
		CiphertextBlob:      append([]byte(nil), wrappedKey...),
		EncryptionAlgorithm: kmstypes.EncryptionAlgorithmSpecSymmetricDefault,
		EncryptionContext:   kmsEncryptionContext(authority),
	})
	if output != nil {
		defer clear(output.Plaintext)
	}
	if err != nil {
		return [32]byte{}, fmt.Errorf("KMS decrypt AES-256 data key: %w", err)
	}
	if output == nil || len(output.Plaintext) != 32 {
		return [32]byte{}, errors.New("KMS decrypt-data-key did not return exactly 32 plaintext bytes")
	}
	var plaintext [32]byte
	copy(plaintext[:], output.Plaintext)
	return plaintext, nil
}

func kmsEncryptionContext(authority []byte) map[string]string {
	digest := sha256.Sum256(authority)
	return map[string]string{
		kmsContextProtocolKey:  kmsContextProtocolValue,
		kmsContextAuthorityKey: hex.EncodeToString(digest[:]),
	}
}

var _ objectstore.DataKeyProvider = (*KMSDataKeyProvider)(nil)
