ALTER TABLE checkpoints
    DROP CONSTRAINT checkpoints_object_size_bounded,
    DROP CONSTRAINT checkpoints_object_media_type_bounded,
    ADD CONSTRAINT checkpoints_object_size_bounded CHECK (
        object_size BETWEEN 1 AND 67174420
    ),
    ADD CONSTRAINT checkpoints_object_media_type_bounded CHECK (
        object_media_type = 'application/vnd.agentserver.codex-checkpoint.v1'
    );
