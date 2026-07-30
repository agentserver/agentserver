ALTER TABLE executor_environments
    DROP CONSTRAINT executor_environments_process_profile_valid;

ALTER TABLE executor_environments
    ADD CONSTRAINT executor_environments_process_profile_valid CHECK (
        outer_profile_version IN (
            'process-v1',
            'process-v1+filesystem-read-v1'
        )
        AND process_methods = ARRAY[
            'process/start', 'process/read', 'process/write', 'process/terminate'
        ]::text[]
    );
