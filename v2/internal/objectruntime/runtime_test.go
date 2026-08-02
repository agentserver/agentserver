package objectruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/agentserver/agentserver/v2/internal/objectstore/awsprovider"
)

func TestParseEnvironmentReturnsCompletePlaintextS3Profile(t *testing.T) {
	configuration := validObjectRuntimeEnvironment()
	config, err := ParseEnvironment(func(name string) string { return configuration[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.ObjectPrefix != "tenant-a/agentserver-v2" ||
		config.Provider.Bucket != "agentserver-objects" || config.Provider.Region != "us-east-1" ||
		config.Provider.Endpoint != "https://s3.example.test/storage" || !config.Provider.UsePathStyle ||
		config.AccessKeyID != "test-access-key" || config.SecretAccessKey != "test-secret-key" {
		t.Fatal("parsed production object config does not match the exact S3 routing and credential inputs")
	}

	typeOfConfig := reflect.TypeFor[Config]()
	fields := make([]string, 0, typeOfConfig.NumField())
	for index := range typeOfConfig.NumField() {
		fields = append(fields, typeOfConfig.Field(index).Name)
	}
	if want := []string{"ObjectPrefix", "Provider", "AccessKeyID", "SecretAccessKey"}; !slices.Equal(fields, want) {
		t.Fatalf("runtime Config fields = %v, want explicit profile %v", fields, want)
	}
}

func TestParseEnvironmentRejectsMissingAndNonCanonicalValues(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"missing prefix":        func(values map[string]string) { delete(values, ObjectPrefixEnvironment) },
		"missing bucket":        func(values map[string]string) { delete(values, S3BucketEnvironment) },
		"missing S3 region":     func(values map[string]string) { delete(values, S3RegionEnvironment) },
		"missing access key":    func(values map[string]string) { delete(values, S3AccessKeyEnvironment) },
		"missing secret key":    func(values map[string]string) { delete(values, S3SecretKeyEnvironment) },
		"prefix whitespace":     func(values map[string]string) { values[ObjectPrefixEnvironment] = " objects" },
		"prefix traversal":      func(values map[string]string) { values[ObjectPrefixEnvironment] = "objects/../escape" },
		"endpoint newline":      func(values map[string]string) { values[S3EndpointEnvironment] += "\n" },
		"path-style case":       func(values map[string]string) { values[S3PathStyleEnvironment] = "TRUE" },
		"path-style padded":     func(values map[string]string) { values[S3PathStyleEnvironment] = " false " },
		"credential whitespace": func(values map[string]string) { values[S3SecretKeyEnvironment] = " secret" },
		"oversize": func(values map[string]string) {
			values[S3SecretKeyEnvironment] = strings.Repeat("x", maximumEnvironmentBytes+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			configuration := validObjectRuntimeEnvironment()
			mutate(configuration)
			if _, err := ParseEnvironment(func(name string) string { return configuration[name] }); err == nil {
				t.Fatal("unsafe production object configuration was accepted")
			}
		})
	}
	if _, err := ParseEnvironment(nil); err == nil {
		t.Fatal("nil production object configuration source was accepted")
	}
}

func TestOpenRejectsInvalidStateBeforeLoadingCredentials(t *testing.T) {
	if _, err := Open(nil, Config{}); err == nil {
		t.Fatal("nil context was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, Config{}); err == nil {
		t.Fatal("cancelled context was accepted")
	}
	if _, err := Open(t.Context(), Config{ObjectPrefix: "agentserver-v2/objects"}); err == nil || !strings.Contains(err.Error(), "S3 bucket") {
		t.Fatalf("invalid provider config error = %v", err)
	}
}

func TestOpenWiresExactProviderRoutingPrefixAndCurrentObjectBound(t *testing.T) {
	configuration := validObjectRuntimeEnvironment()
	config, err := ParseEnvironment(func(name string) string { return configuration[name] })
	if err != nil {
		t.Fatal(err)
	}
	blobs := &runtimeTestBlobStore{}
	loadCalls := 0
	store, err := open(t.Context(), config, func(
		_ context.Context,
		providerConfig awsprovider.S3Config,
		accessKeyID string,
		secretAccessKey string,
	) (objectstore.ImmutableBlobStore, error) {
		loadCalls++
		if !reflect.DeepEqual(providerConfig, config.Provider) || accessKeyID != config.AccessKeyID || secretAccessKey != config.SecretAccessKey {
			t.Fatal("provider loader did not receive the exact routing and credential inputs")
		}
		return blobs, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte("exact production object factory")
	scope := objectstore.Scope{
		WorkspaceID: "10000000-0000-4000-8000-000000000001", Kind: objectstore.KindUserPrompt,
		Descriptor: objectstore.Descriptor{
			ObjectID: "20000000-0000-4000-8000-000000000002", SHA256: sha256.Sum256(contents),
			Size: int64(len(contents)), MediaType: "text/plain; charset=utf-8",
		},
	}
	if err := store.Put(t.Context(), scope, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	if loadCalls != 1 || blobs.key != "tenant-a/agentserver-v2/10000000-0000-4000-8000-000000000001/user-prompt/20000000-0000-4000-8000-000000000002" ||
		blobs.size != int64(len(contents)) || blobs.consumed != blobs.size || !bytes.Equal(blobs.contents, contents) {
		t.Fatalf("factory write = calls %d, key %q, size %d, consumed %d", loadCalls, blobs.key, blobs.size, blobs.consumed)
	}

	tooLarge := scope
	tooLarge.Descriptor.ObjectID = "30000000-0000-4000-8000-000000000003"
	tooLarge.Descriptor.Size = checkpoint.MaximumArtifactBytes + 1
	if err := store.Put(t.Context(), tooLarge, bytes.NewReader(nil)); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("current object bound error = %v", err)
	}
	if loadCalls != 1 {
		t.Fatalf("provider loader calls after semantic write = %d", loadCalls)
	}

	providerFailure := errors.New("provider unavailable")
	if _, err := open(t.Context(), config, func(
		context.Context,
		awsprovider.S3Config,
		string,
		string,
	) (objectstore.ImmutableBlobStore, error) {
		return nil, providerFailure
	}); !errors.Is(err, providerFailure) {
		t.Fatalf("provider load failure = %v", err)
	}
	if _, err := open(t.Context(), config, nil); err == nil {
		t.Fatal("nil provider loader was accepted")
	}
}

func validObjectRuntimeEnvironment() map[string]string {
	return map[string]string{
		ObjectPrefixEnvironment: "tenant-a/agentserver-v2",
		S3BucketEnvironment:     "agentserver-objects",
		S3RegionEnvironment:     "us-east-1",
		S3EndpointEnvironment:   "https://s3.example.test/storage",
		S3PathStyleEnvironment:  "true",
		S3AccessKeyEnvironment:  "test-access-key",
		S3SecretKeyEnvironment:  "test-secret-key",
	}
}

type runtimeTestBlobStore struct {
	key      string
	size     int64
	consumed int64
	contents []byte
}

func (store *runtimeTestBlobStore) PutIfAbsent(
	_ context.Context,
	key string,
	size int64,
	source io.Reader,
) (objectstore.PutResult, error) {
	store.key = key
	store.size = size
	contents, err := io.ReadAll(source)
	consumed := int64(len(contents))
	store.consumed = consumed
	store.contents = contents
	return objectstore.PutResult{Created: err == nil}, err
}

func (*runtimeTestBlobStore) Open(context.Context, string) (objectstore.Blob, error) {
	return objectstore.Blob{}, errors.New("unexpected open")
}
