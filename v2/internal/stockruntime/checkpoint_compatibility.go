package stockruntime

// PreviousManifestSHA256 identifies the previously deployed 0.146.0 bundle.
// This is a one-way native-rollout upgrade, not permission to replay history
// into an arbitrary runtime or to downgrade a new checkpoint.
const PreviousManifestSHA256 = "5b7297d0a4e5eb294dc201ce2e82ae8f73f27b51c3360c8737e4338e46e3d128"

// CanResumeCheckpoint keeps exact-runtime restores and the single transition
// characterized by TestAppServerA09Upgrade0146To0160Checkpoint. The caller
// must still authenticate the checkpoint against its ORIGINAL runtime identity,
// session, catalog and object authority. New checkpoints use the target identity.
func CanResumeCheckpoint(source, target string, sourceAllowlist, targetAllowlist int64) bool {
	if sourceAllowlist != targetAllowlist {
		return false
	}
	if source == target {
		return true
	}
	return source == PreviousManifestSHA256 && target == ManifestSHA256 &&
		sourceAllowlist == CheckpointAllowlistVersion
}
