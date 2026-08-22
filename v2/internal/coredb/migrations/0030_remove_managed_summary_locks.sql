ALTER TABLE managed_sandboxes
    DROP CONSTRAINT managed_sandboxes_digests_sha256,
    DROP COLUMN runtime_profile_digest,
    DROP COLUMN pack_set_digest,
    ADD CONSTRAINT managed_sandboxes_last_error_digest_sha256 CHECK (
        last_error_digest IS NULL
        OR pg_catalog.octet_length(last_error_digest) = 32
    );

ALTER TABLE checkpoints
    DROP CONSTRAINT checkpoints_pack_set_digest_sha256,
    DROP COLUMN pack_set_digest;

DROP INDEX run_launch_states_managed_sandbox_profile_idx;

ALTER TABLE run_launch_states
    DROP CONSTRAINT run_launch_states_managed_sandbox_complete,
    DROP COLUMN managed_sandbox_profile_id,
    DROP COLUMN managed_sandbox_binding_sha256,
    ADD CONSTRAINT run_launch_states_managed_sandbox_complete CHECK (
        (
            managed_sandbox_setting_version IS NULL
            AND managed_sandbox_region IS NULL
            AND managed_sandbox_environment_id IS NULL
        )
        OR
        (
            managed_sandbox_setting_version > 0
            AND managed_sandbox_region IN ('cn', 'boe', 'i18n-bd', 'i18n-tt')
            AND managed_sandbox_environment_id IS NOT NULL
        )
    );

CREATE INDEX run_launch_states_managed_sandbox_region_idx
    ON run_launch_states(managed_sandbox_region, managed_sandbox_environment_id, run_id)
    WHERE managed_sandbox_region IS NOT NULL;

ALTER TABLE executor_environments
    DROP CONSTRAINT executor_environments_owner_policy_sha256_exact,
    ALTER COLUMN owner_policy_sha256 DROP NOT NULL,
    ADD CONSTRAINT executor_environments_owner_policy_sha256_optional CHECK (
        owner_policy_sha256 IS NULL
        OR pg_catalog.octet_length(owner_policy_sha256) = 32
    );

UPDATE executor_environments
SET owner_policy_sha256 = NULL
WHERE backend_kind = 'tae';
