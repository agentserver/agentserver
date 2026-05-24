-- Drop the response/pairing-related columns on exec_audit_calls now that
-- the gateway only records requests (no response pairing). The matching
-- code paths (UpdateAuditCallEnd, CallEnd WAL records, response payload
-- endpoint) have all been removed.

DROP INDEX IF EXISTS exec_audit_calls_errors;

ALTER TABLE exec_audit_calls
  DROP COLUMN IF EXISTS response_payload_id,
  DROP COLUMN IF EXISTS response_size,
  DROP COLUMN IF EXISTS response_sha256,
  DROP COLUMN IF EXISTS is_error,
  DROP COLUMN IF EXISTS error_summary,
  DROP COLUMN IF EXISTS completed_at,
  DROP COLUMN IF EXISTS duration_ms;
