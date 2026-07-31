package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestCheckpointArtifactRoundTripIsExactAndVerified(t *testing.T) {
	manifest, rollout := checkpointTestManifest()
	var artifact bytes.Buffer
	descriptor, err := WriteArtifact(&artifact, manifest, bytes.NewReader(rollout))
	if err != nil {
		t.Fatal(err)
	}
	objectDigest := sha256.Sum256(artifact.Bytes())
	if descriptor.MediaType != ArtifactMediaType || descriptor.SizeBytes != int64(artifact.Len()) ||
		descriptor.SHA256 != hex.EncodeToString(objectDigest[:]) {
		t.Fatalf("artifact descriptor = %+v", descriptor)
	}
	wantManifestDigest, err := Digest(mustCheckpointCanonical(t, manifest))
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ManifestDigest != wantManifestDigest {
		t.Fatalf("manifest digest = %q, want %q", descriptor.ManifestDigest, wantManifestDigest)
	}
	var restored bytes.Buffer
	err = ReadArtifact(bytes.NewReader(artifact.Bytes()), int64(artifact.Len()), func(got Manifest, canonical []byte, source io.Reader) error {
		if got.CheckpointID != manifest.CheckpointID || !bytes.Equal(canonical, mustCheckpointCanonical(t, manifest)) {
			t.Fatal("artifact changed its canonical manifest")
		}
		_, err := CopyVerifiedRollout(&restored, source, got.Files[0])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), rollout) {
		t.Fatal("artifact changed rollout bytes")
	}
}

func TestCheckpointArtifactRejectsDriftMalformedJSONLAndTrailingBytes(t *testing.T) {
	manifest, rollout := checkpointTestManifest()
	tests := []struct {
		name   string
		mutate func(*Manifest, *[]byte)
		want   string
	}{
		{name: "digest", mutate: func(m *Manifest, _ *[]byte) { m.Files[0].SHA256 = strings.Repeat("0", 64) }, want: "digest"},
		{name: "size", mutate: func(m *Manifest, _ *[]byte) { m.Files[0].SizeBytes++ }, want: "size"},
		{name: "malformed JSONL", mutate: func(m *Manifest, contents *[]byte) {
			*contents = []byte("not-json\n")
			digest := sha256.Sum256(*contents)
			m.Files[0].SizeBytes = int64(len(*contents))
			m.Files[0].SHA256 = hex.EncodeToString(digest[:])
		}, want: "JSON value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := manifest
			candidate.Files = append([]File(nil), manifest.Files...)
			contents := append([]byte(nil), rollout...)
			test.mutate(&candidate, &contents)
			var artifact bytes.Buffer
			if _, err := WriteArtifact(&artifact, candidate, bytes.NewReader(contents)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("WriteArtifact() error = %v, want %q", err, test.want)
			}
		})
	}

	var artifact bytes.Buffer
	if _, err := WriteArtifact(&artifact, manifest, bytes.NewReader(rollout)); err != nil {
		t.Fatal(err)
	}
	withTrailing := append(append([]byte(nil), artifact.Bytes()...), 'x')
	err := ReadArtifact(bytes.NewReader(withTrailing), int64(artifact.Len()), func(got Manifest, _ []byte, source io.Reader) error {
		_, err := CopyVerifiedRollout(io.Discard, source, got.Files[0])
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing-byte error = %v", err)
	}
}

func TestStageRolloutValidatesAndHashesWhileCopying(t *testing.T) {
	rollout := []byte("{\"type\":\"session_meta\"}\n{\"type\":\"response_item\"}\n")
	var staged bytes.Buffer
	descriptor, err := StageRollout(&staged, bytes.NewReader(rollout), int64(len(rollout)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(rollout)
	if descriptor.SizeBytes != int64(len(rollout)) || descriptor.SHA256 != hex.EncodeToString(digest[:]) || !bytes.Equal(staged.Bytes(), rollout) {
		t.Fatalf("staged rollout descriptor/bytes = %+v / %q", descriptor, staged.Bytes())
	}
	for name, source := range map[string][]byte{
		"invalid JSONL": []byte("not-json\n"),
		"size drift":    append(append([]byte(nil), rollout...), 'x'),
	} {
		t.Run(name, func(t *testing.T) {
			var destination bytes.Buffer
			if _, err := StageRollout(&destination, bytes.NewReader(source), int64(len(rollout))); err == nil {
				t.Fatal("StageRollout() unexpectedly succeeded")
			}
		})
	}
}
