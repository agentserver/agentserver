ALTER TABLE workspace_credential_use_events
    DROP CONSTRAINT workspace_credential_use_events_stage_valid,
    ADD CONSTRAINT workspace_credential_use_events_stage_valid
        CHECK (stage IN ('materialize', 'egress', 'process_env'));
