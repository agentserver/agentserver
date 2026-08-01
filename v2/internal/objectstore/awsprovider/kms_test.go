package awsprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func TestKMSDataKeyProviderGenerateBindsAuthorityAndClearsSDKBuffer(t *testing.T) {
	authority := []byte("complete provider-neutral object authority")
	plaintext := bytes.Repeat([]byte{0x31}, 32)
	wrapper := []byte("wrapped-key")
	var captured *kms.GenerateDataKeyInput
	client := &fakeKMSClient{generate: func(_ context.Context, input *kms.GenerateDataKeyInput) (*kms.GenerateDataKeyOutput, error) {
		captured = input
		return &kms.GenerateDataKeyOutput{
			KeyId:     aws.String("arn:aws:kms:region:account:key/generated"),
			Plaintext: plaintext, CiphertextBlob: wrapper,
		}, nil
	}}
	provider, err := NewKMSDataKeyProvider(client, "alias/agentserver-objects")
	if err != nil {
		t.Fatal(err)
	}
	generated, err := provider.GenerateDataKey(t.Context(), authority)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(authority)
	wantContext := map[string]string{
		kmsContextProtocolKey:  kmsContextProtocolValue,
		kmsContextAuthorityKey: hex.EncodeToString(digest[:]),
	}
	if captured == nil || aws.ToString(captured.KeyId) != "alias/agentserver-objects" ||
		captured.KeySpec != kmstypes.DataKeySpecAes256 || !maps.Equal(captured.EncryptionContext, wantContext) {
		t.Fatalf("GenerateDataKey input = %+v", captured)
	}
	if generated.KeyID != "arn:aws:kms:region:account:key/generated" ||
		!bytes.Equal(generated.Plaintext[:], bytes.Repeat([]byte{0x31}, 32)) ||
		!bytes.Equal(generated.WrappedKey, []byte("wrapped-key")) {
		t.Fatalf("GenerateDataKey() = %+v", generated)
	}
	if !allZero(plaintext) {
		t.Fatal("GenerateDataKey did not clear the SDK plaintext buffer")
	}
	wrapper[0] ^= 0xff
	if !bytes.Equal(generated.WrappedKey, []byte("wrapped-key")) {
		t.Fatal("GenerateDataKey returned the SDK ciphertext buffer without copying")
	}
}

func TestKMSDataKeyProviderDecryptUsesStoredKeyAndClearsSDKBuffer(t *testing.T) {
	authority := []byte("bound authority")
	wrapper := []byte("persisted wrapped key")
	plaintext := bytes.Repeat([]byte{0x52}, 32)
	var captured *kms.DecryptInput
	client := &fakeKMSClient{decrypt: func(_ context.Context, input *kms.DecryptInput) (*kms.DecryptOutput, error) {
		captured = input
		return &kms.DecryptOutput{Plaintext: plaintext}, nil
	}}
	provider, err := NewKMSDataKeyProvider(client, "alias/current-write-key")
	if err != nil {
		t.Fatal(err)
	}
	key, err := provider.DecryptDataKey(t.Context(), "arn:aws:kms:region:account:key/stored", wrapper, authority)
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil || aws.ToString(captured.KeyId) != "arn:aws:kms:region:account:key/stored" ||
		captured.EncryptionAlgorithm != kmstypes.EncryptionAlgorithmSpecSymmetricDefault ||
		!bytes.Equal(captured.CiphertextBlob, wrapper) ||
		!maps.Equal(captured.EncryptionContext, kmsEncryptionContext(authority)) {
		t.Fatalf("Decrypt input = %+v", captured)
	}
	if !bytes.Equal(key[:], bytes.Repeat([]byte{0x52}, 32)) {
		t.Fatalf("DecryptDataKey() = %x", key)
	}
	if !allZero(plaintext) {
		t.Fatal("DecryptDataKey did not clear the SDK plaintext buffer")
	}
	wrapper[0] ^= 0xff
	if bytes.Equal(captured.CiphertextBlob, wrapper) {
		t.Fatal("DecryptDataKey did not copy the caller's wrapped key")
	}
}

func TestKMSDataKeyProviderClearsPlaintextOnProviderError(t *testing.T) {
	generatePlaintext := bytes.Repeat([]byte{1}, 32)
	decryptPlaintext := bytes.Repeat([]byte{2}, 32)
	providerErr := errors.New("provider unavailable")
	client := &fakeKMSClient{
		generate: func(context.Context, *kms.GenerateDataKeyInput) (*kms.GenerateDataKeyOutput, error) {
			return &kms.GenerateDataKeyOutput{Plaintext: generatePlaintext}, providerErr
		},
		decrypt: func(context.Context, *kms.DecryptInput) (*kms.DecryptOutput, error) {
			return &kms.DecryptOutput{Plaintext: decryptPlaintext}, providerErr
		},
	}
	provider, err := NewKMSDataKeyProvider(client, "alias/key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GenerateDataKey(t.Context(), []byte("authority")); !errors.Is(err, providerErr) {
		t.Fatalf("GenerateDataKey() = %v", err)
	}
	if _, err := provider.DecryptDataKey(t.Context(), "stored-key", []byte("wrapped"), []byte("authority")); !errors.Is(err, providerErr) {
		t.Fatalf("DecryptDataKey() = %v", err)
	}
	if !allZero(generatePlaintext) || !allZero(decryptPlaintext) {
		t.Fatal("provider error path retained an SDK plaintext buffer")
	}
}

func TestKMSDataKeyProviderRejectsMalformedGenerateOutput(t *testing.T) {
	tests := []struct {
		name   string
		output *kms.GenerateDataKeyOutput
	}{
		{name: "nil output"},
		{name: "nil key ID", output: &kms.GenerateDataKeyOutput{Plaintext: make([]byte, 32), CiphertextBlob: []byte{1}}},
		{name: "short plaintext", output: &kms.GenerateDataKeyOutput{KeyId: aws.String("key"), Plaintext: make([]byte, 31), CiphertextBlob: []byte{1}}},
		{name: "long plaintext", output: &kms.GenerateDataKeyOutput{KeyId: aws.String("key"), Plaintext: make([]byte, 33), CiphertextBlob: []byte{1}}},
		{name: "empty wrapper", output: &kms.GenerateDataKeyOutput{KeyId: aws.String("key"), Plaintext: make([]byte, 32)}},
		{name: "oversize wrapper", output: &kms.GenerateDataKeyOutput{KeyId: aws.String("key"), Plaintext: make([]byte, 32), CiphertextBlob: make([]byte, maximumWrappedKeyBytes+1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeKMSClient{generate: func(context.Context, *kms.GenerateDataKeyInput) (*kms.GenerateDataKeyOutput, error) {
				return test.output, nil
			}}
			provider, err := NewKMSDataKeyProvider(client, "alias/key")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.GenerateDataKey(t.Context(), []byte("authority")); err == nil {
				t.Fatal("GenerateDataKey accepted malformed KMS output")
			}
			if test.output != nil && !allZero(test.output.Plaintext) {
				t.Fatal("GenerateDataKey retained malformed KMS plaintext")
			}
		})
	}
}

func TestKMSDataKeyProviderRejectsMalformedDecryptOutput(t *testing.T) {
	for _, size := range []int{-1, 0, 31, 33} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			var output *kms.DecryptOutput
			if size >= 0 {
				output = &kms.DecryptOutput{Plaintext: make([]byte, size)}
			}
			client := &fakeKMSClient{decrypt: func(context.Context, *kms.DecryptInput) (*kms.DecryptOutput, error) {
				return output, nil
			}}
			provider, err := NewKMSDataKeyProvider(client, "alias/key")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.DecryptDataKey(t.Context(), "stored-key", []byte("wrapped"), []byte("authority")); err == nil {
				t.Fatal("DecryptDataKey accepted malformed KMS output")
			}
		})
	}
}

func TestKMSDataKeyProviderValidatesInputsWithoutCallingKMS(t *testing.T) {
	client := &fakeKMSClient{}
	if _, err := NewKMSDataKeyProvider(nil, "alias/key"); err == nil {
		t.Fatal("NewKMSDataKeyProvider accepted nil client")
	}
	if _, err := NewKMSDataKeyProvider(client, " alias/key "); err == nil {
		t.Fatal("NewKMSDataKeyProvider accepted invalid key ID")
	}
	provider, err := NewKMSDataKeyProvider(client, "alias/key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GenerateDataKey(nil, []byte("authority")); err == nil {
		t.Fatal("GenerateDataKey accepted nil context")
	}
	if _, err := provider.GenerateDataKey(t.Context(), nil); err == nil {
		t.Fatal("GenerateDataKey accepted empty authority")
	}
	if _, err := provider.DecryptDataKey(nil, "key", []byte{1}, []byte{1}); err == nil {
		t.Fatal("DecryptDataKey accepted nil context")
	}
	if _, err := provider.DecryptDataKey(t.Context(), "", []byte{1}, []byte{1}); err == nil {
		t.Fatal("DecryptDataKey accepted empty stored key ID")
	}
	if _, err := provider.DecryptDataKey(t.Context(), "key", nil, []byte{1}); err == nil {
		t.Fatal("DecryptDataKey accepted empty wrapped key")
	}
	if _, err := provider.DecryptDataKey(t.Context(), "key", []byte{1}, nil); err == nil {
		t.Fatal("DecryptDataKey accepted empty authority")
	}
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

type fakeKMSClient struct {
	generate func(context.Context, *kms.GenerateDataKeyInput) (*kms.GenerateDataKeyOutput, error)
	decrypt  func(context.Context, *kms.DecryptInput) (*kms.DecryptOutput, error)
}

func (client *fakeKMSClient) GenerateDataKey(
	ctx context.Context,
	input *kms.GenerateDataKeyInput,
	_ ...func(*kms.Options),
) (*kms.GenerateDataKeyOutput, error) {
	if client.generate == nil {
		return nil, errors.New("unexpected GenerateDataKey call")
	}
	return client.generate(ctx, input)
}

func (client *fakeKMSClient) Decrypt(
	ctx context.Context,
	input *kms.DecryptInput,
	_ ...func(*kms.Options),
) (*kms.DecryptOutput, error) {
	if client.decrypt == nil {
		return nil, errors.New("unexpected Decrypt call")
	}
	return client.decrypt(ctx, input)
}
