package checkpoint

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const artifactPrefixBytes = 20

var artifactMagic = [16]byte{'A', 'G', 'E', 'N', 'T', 'S', 'E', 'R', 'V', 'E', 'R', 'C', 'P', 0, 0, 1}

// MaximumArtifactBytes is the hard worker-side object bound. Object pointers
// larger than this are rejected before any checkpoint bytes are read.
const MaximumArtifactBytes = int64(artifactPrefixBytes+MaximumManifestBytes) + MaximumRolloutBytes

type ArtifactDescriptor struct {
	ManifestDigest string
	SHA256         string
	SizeBytes      int64
	MediaType      string
}

// WriteArtifact emits the exact v1 framing:
//
//	16-byte magic | uint32 big-endian manifest length | canonical manifest | rollout bytes
//
// There is no general archive entry layer. The manifest permits exactly one
// regular rollout, and any byte after its signed length makes the write fail.
func WriteArtifact(destination io.Writer, manifest Manifest, rollout io.Reader) (ArtifactDescriptor, error) {
	if destination == nil || rollout == nil {
		return ArtifactDescriptor{}, errors.New("checkpoint artifact destination and rollout are required")
	}
	canonical, err := CanonicalBytes(manifest)
	if err != nil {
		return ArtifactDescriptor{}, err
	}
	manifestDigest, err := Digest(canonical)
	if err != nil {
		return ArtifactDescriptor{}, err
	}
	hasher := sha256.New()
	counter := &countingWriter{}
	output := io.MultiWriter(destination, hasher, counter)
	var prefix [artifactPrefixBytes]byte
	copy(prefix[:16], artifactMagic[:])
	binary.BigEndian.PutUint32(prefix[16:], uint32(len(canonical)))
	if _, err := output.Write(prefix[:]); err != nil {
		return ArtifactDescriptor{}, fmt.Errorf("write checkpoint artifact prefix: %w", err)
	}
	if _, err := output.Write(canonical); err != nil {
		return ArtifactDescriptor{}, fmt.Errorf("write checkpoint artifact manifest: %w", err)
	}
	if _, err := CopyVerifiedRollout(output, rollout, manifest.Files[0]); err != nil {
		return ArtifactDescriptor{}, fmt.Errorf("write checkpoint artifact rollout: %w", err)
	}
	return ArtifactDescriptor{
		ManifestDigest: manifestDigest,
		SHA256:         hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:      counter.written,
		MediaType:      ArtifactMediaType,
	}, nil
}

// ReadArtifact parses the bounded framing and gives consume an exact reader
// for the sole rollout. consume must read it through EOF. The caller is
// expected to have independently verified the outer object size and digest.
func ReadArtifact(source io.Reader, objectSize int64, consume func(Manifest, []byte, io.Reader) error) error {
	if source == nil || consume == nil {
		return errors.New("checkpoint artifact source and consumer are required")
	}
	if objectSize < artifactPrefixBytes+1 || objectSize > MaximumArtifactBytes {
		return errors.New("checkpoint artifact object size is outside the supported bound")
	}
	var prefix [artifactPrefixBytes]byte
	if _, err := io.ReadFull(source, prefix[:]); err != nil {
		return fmt.Errorf("read checkpoint artifact prefix: %w", err)
	}
	if !bytes.Equal(prefix[:16], artifactMagic[:]) {
		return errors.New("checkpoint artifact magic or version is invalid")
	}
	manifestBytes := int64(binary.BigEndian.Uint32(prefix[16:]))
	if manifestBytes < 1 || manifestBytes > MaximumManifestBytes || artifactPrefixBytes+manifestBytes >= objectSize {
		return errors.New("checkpoint artifact manifest length is invalid")
	}
	canonical := make([]byte, manifestBytes)
	if _, err := io.ReadFull(source, canonical); err != nil {
		return fmt.Errorf("read checkpoint artifact manifest: %w", err)
	}
	manifest, err := ParseCanonical(canonical)
	if err != nil {
		return err
	}
	remaining := objectSize - artifactPrefixBytes - manifestBytes
	if remaining != manifest.Files[0].SizeBytes {
		return errors.New("checkpoint artifact size does not match its sole manifest entry")
	}
	rollout := &io.LimitedReader{R: source, N: remaining}
	if err := consume(manifest, canonical, rollout); err != nil {
		return err
	}
	if rollout.N != 0 {
		return errors.New("checkpoint artifact consumer did not consume the complete rollout")
	}
	var extra [1]byte
	read, err := source.Read(extra[:])
	if read != 0 || !errors.Is(err, io.EOF) {
		return errors.New("checkpoint artifact contains trailing bytes")
	}
	return nil
}

// CopyVerifiedRollout validates the v1 JSONL profile, exact size, and digest
// while streaming bytes to destination. No rollout content is retained.
func CopyVerifiedRollout(destination io.Writer, source io.Reader, expected File) (int64, error) {
	if destination == nil || source == nil {
		return 0, errors.New("checkpoint rollout destination and source are required")
	}
	if err := expected.Validate(1); err != nil {
		return 0, err
	}
	hasher := sha256.New()
	counter := &countingWriter{}
	limited := io.LimitReader(source, expected.SizeBytes+1)
	tee := io.TeeReader(limited, io.MultiWriter(destination, hasher, counter))
	if err := validateRolloutJSONL(tee); err != nil {
		return counter.written, err
	}
	if counter.written != expected.SizeBytes {
		return counter.written, errors.New("checkpoint rollout bytes do not match the manifest size")
	}
	want, err := hex.DecodeString(expected.SHA256)
	if err != nil || len(want) != sha256.Size || hex.EncodeToString(want) != expected.SHA256 {
		return counter.written, errors.New("checkpoint rollout manifest digest is invalid")
	}
	if subtle.ConstantTimeCompare(hasher.Sum(nil), want) != 1 {
		return counter.written, errors.New("checkpoint rollout bytes do not match the manifest digest")
	}
	return counter.written, nil
}

func validateRolloutJSONL(source io.Reader) error {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), MaximumRolloutLineBytes)
	records := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 || !json.Valid(line) {
			return fmt.Errorf("checkpoint rollout record %d is not one non-empty JSON value", records+1)
		}
		records++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan checkpoint rollout JSONL: %w", err)
	}
	if records == 0 {
		return errors.New("checkpoint rollout contains no JSONL records")
	}
	return nil
}

type countingWriter struct{ written int64 }

func (writer *countingWriter) Write(contents []byte) (int, error) {
	writer.written += int64(len(contents))
	return len(contents), nil
}
