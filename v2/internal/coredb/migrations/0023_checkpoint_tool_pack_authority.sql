ALTER TABLE checkpoints
    ADD COLUMN pack_set_digest bytea,
    ADD CONSTRAINT checkpoints_pack_set_digest_sha256 CHECK (
        pack_set_digest IS NULL
        OR (
            pg_catalog.octet_length(pack_set_digest) = 32
            AND pack_set_digest <> pg_catalog.decode(pg_catalog.repeat('00', 32), 'hex')
        )
    );
