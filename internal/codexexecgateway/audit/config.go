package audit

import (
	"os"
	"strconv"
	"time"
)

// Config controls every knob of the audit subsystem. NewConfigFromEnv
// reads CXG_AUDIT_* env vars with reasonable defaults. Zero-value
// Config is safe (Enabled=false → use NewNoopRecorder()).
type Config struct {
	Enabled             bool
	WALDir              string
	WALFsyncInterval    time.Duration
	WALFsyncRecords     int
	WALFileMaxBytes     int64
	WALDiskQuotaBytes   int64
	WALOverflow         string // "fail" | "drop"
	PayloadMaxBytes     int
	UploadURL           string
	UploadSecret        string
	UploadBatchBytes    int
	UploadBatchRecords  int
	UploadFlushInterval time.Duration
	GatewayID           string
}

func NewConfigFromEnv() Config {
	return Config{
		Enabled:             envBool("CXG_AUDIT_ENABLED", false),
		WALDir:              envStr("CXG_AUDIT_WAL_DIR", "/var/cxg-audit"),
		WALFsyncInterval:    envDur("CXG_AUDIT_WAL_FSYNC_INTERVAL", 100*time.Millisecond),
		WALFsyncRecords:     envInt("CXG_AUDIT_WAL_FSYNC_RECORDS", 256),
		WALFileMaxBytes:     envInt64("CXG_AUDIT_WAL_FILE_MAX_BYTES", 1<<30),    // 1 GiB
		WALDiskQuotaBytes:   envInt64("CXG_AUDIT_WAL_DISK_QUOTA_BYTES", 10<<30), // 10 GiB
		WALOverflow:         envStr("CXG_AUDIT_WAL_OVERFLOW", "fail"),
		PayloadMaxBytes:     envInt("CXG_AUDIT_PAYLOAD_MAX_BYTES", 4<<20), // 4 MiB
		UploadURL:           envStr("CXG_AUDIT_UPLOAD_URL", ""),
		UploadSecret:        envStr("CXG_AUDIT_UPLOAD_SECRET", ""),
		UploadBatchBytes:    envInt("CXG_AUDIT_UPLOAD_BATCH_BYTES", 1<<20), // 1 MiB
		UploadBatchRecords:  envInt("CXG_AUDIT_UPLOAD_BATCH_RECORDS", 200),
		UploadFlushInterval: envDur("CXG_AUDIT_UPLOAD_FLUSH_INTERVAL", time.Second),
		GatewayID:           envStr("CXG_AUDIT_GATEWAY_ID", "cxg"),
	}
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
