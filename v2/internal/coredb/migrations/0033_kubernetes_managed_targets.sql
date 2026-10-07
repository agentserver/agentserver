-- Expand provider identities without relabeling historical TAE rows. Existing
-- runs remain frozen to their original backend; Kubernetes gets new profiles.
ALTER TABLE workspace_managed_sandbox_settings
    DROP CONSTRAINT workspace_managed_sandbox_settings_region_valid,
    ADD CONSTRAINT workspace_managed_sandbox_settings_region_valid CHECK (region IN ('cn','boe','i18n-bd','i18n-tt','sg'));
ALTER TABLE workspace_managed_sandbox_setting_events
    DROP CONSTRAINT workspace_managed_sandbox_setting_events_previous_valid,
    DROP CONSTRAINT workspace_managed_sandbox_setting_events_current_valid,
    ADD CONSTRAINT workspace_managed_sandbox_setting_events_previous_valid CHECK (previous_region IN ('cn','boe','i18n-bd','i18n-tt','sg')),
    ADD CONSTRAINT workspace_managed_sandbox_setting_events_current_valid CHECK (current_region IN ('cn','boe','i18n-bd','i18n-tt','sg'));
ALTER TABLE run_launch_states
    DROP CONSTRAINT run_launch_states_managed_sandbox_complete,
    ADD CONSTRAINT run_launch_states_managed_sandbox_complete CHECK (
        (managed_sandbox_setting_version IS NULL AND managed_sandbox_region IS NULL AND managed_sandbox_environment_id IS NULL) OR
        (managed_sandbox_setting_version > 0 AND managed_sandbox_region IN ('cn','boe','i18n-bd','i18n-tt','sg') AND managed_sandbox_environment_id IS NOT NULL)
    );

ALTER TABLE executor_environments
    DROP CONSTRAINT executor_environments_backend_kind_valid,
    DROP CONSTRAINT executor_environments_backend_root_valid,
    ADD CONSTRAINT executor_environments_backend_kind_valid CHECK (backend_kind IN ('agentx', 'tae', 'k8s')),
    ADD CONSTRAINT executor_environments_backend_root_valid CHECK (
        (backend_kind = 'agentx' AND root_descriptor->>'kind' = 'local') OR
        (backend_kind IN ('tae', 'k8s') AND root_descriptor->>'kind' = 'managed'
         AND platform = 'linux-amd64' AND insecure_dev = false)
    );

ALTER TABLE managed_sandboxes
    DROP CONSTRAINT managed_sandboxes_provider_valid,
    ADD CONSTRAINT managed_sandboxes_provider_valid CHECK (provider_kind IN ('tae', 'k8s'));

ALTER TABLE executions
    DROP CONSTRAINT executions_target_complete,
    ADD CONSTRAINT executions_target_complete CHECK (
        (target_kind IS NULL AND target_id IS NULL AND target_generation IS NULL) OR
        (target_kind IN ('agentx', 'tae', 'k8s') AND target_id IS NOT NULL
         AND (target_generation IS NULL OR target_generation > 0))
    );

ALTER TABLE execution_operations
    DROP CONSTRAINT execution_operations_target_complete,
    DROP CONSTRAINT execution_operations_dispatch_matches_status,
    ADD CONSTRAINT execution_operations_target_complete CHECK (
        (target_kind IS NULL AND target_id IS NULL AND target_generation IS NULL) OR
        (target_kind IN ('agentx', 'tae', 'k8s') AND target_id IS NOT NULL
         AND (target_generation IS NULL OR target_generation > 0))
    ),
    ADD CONSTRAINT execution_operations_dispatch_matches_status CHECK (
        (status IN ('prepared', 'skipped') AND connection_generation IS NULL AND dispatched_at IS NULL) OR
        (status IN ('dispatching', 'acknowledged', 'succeeded', 'failed', 'cancelled', 'unknown')
         AND target_kind IN ('agentx', 'tae', 'k8s') AND target_id IS NOT NULL
         AND target_generation > 0 AND dispatched_at IS NOT NULL
         AND ((target_kind = 'agentx' AND connection_generation = target_generation) OR
              (target_kind IN ('tae', 'k8s') AND connection_generation IS NULL)))
    );
