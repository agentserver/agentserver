-- 031_drop_operations.sql
-- Drop the legacy operations table. The codex-app-gateway oplog interceptor
-- was never wired in production, so this table has remained empty since
-- introduction. Audit responsibility moves to the new exec_audit_* tables
-- (see docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md).

DROP TABLE IF EXISTS operations;
