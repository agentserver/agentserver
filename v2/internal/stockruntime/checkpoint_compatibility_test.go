package stockruntime

import "testing"

func TestCheckpointRuntimeTransitions(t *testing.T) {
	for _, tt := range []struct {
		name, source, target             string
		sourceAllowlist, targetAllowlist int64
		want                             bool
	}{
		{"same", ManifestSHA256, ManifestSHA256, 1, 1, true},
		{"upgrade", PreviousManifestSHA256, ManifestSHA256, 1, 1, true},
		{"downgrade", ManifestSHA256, PreviousManifestSHA256, 1, 1, false},
		{"unknown", "unknown", ManifestSHA256, 1, 1, false},
		{"changed allowlist", PreviousManifestSHA256, ManifestSHA256, 1, 2, false},
		{"uncharacterized allowlist", PreviousManifestSHA256, ManifestSHA256, 2, 2, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanResumeCheckpoint(tt.source, tt.target, tt.sourceAllowlist, tt.targetAllowlist); got != tt.want {
				t.Fatalf("CanResumeCheckpoint = %t, want %t", got, tt.want)
			}
		})
	}
}
