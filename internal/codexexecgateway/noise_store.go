package codexexecgateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	"github.com/google/uuid"
)

// NoiseExecutorRegistration is the typed row backing
// noise_executor_registrations. Suite/pubkey strings are kept as the
// wire base64 form so re-emission needs no round-trip decode.
type NoiseExecutorRegistration struct {
	RegistrationID    string
	EnvID             string
	PublicKey         noise.PublicKey
	CreatedAt         time.Time
	LastSeenAt        time.Time
}

// UpsertNoiseExecutorRegistration is idempotent on (env_id, pubkey
// fingerprint). The same executor process re-registering after a
// transient disconnect gets back the same registration_id, so any
// stream prologues bound to that id stay valid across reconnects.
func (s *Store) UpsertNoiseExecutorRegistration(ctx context.Context, envID string, pk noise.PublicKey) (NoiseExecutorRegistration, error) {
	fp := pubkeyFingerprint(pk)

	// Try insert first; on conflict bump last_seen_at and return the
	// existing registration_id. Done as a single round-trip via
	// ON CONFLICT.
	newID := "exr_" + uuid.NewString()
	const q = `
		INSERT INTO noise_executor_registrations
			(registration_id, env_id, suite, x25519_public_key, mlkem768_public_key, pubkey_fingerprint)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (env_id, pubkey_fingerprint)
		DO UPDATE SET last_seen_at = NOW()
		RETURNING registration_id, env_id, suite, x25519_public_key, mlkem768_public_key, created_at, last_seen_at
	`
	row := s.db.QueryRowContext(ctx, q, newID, envID, pk.Suite, pk.X25519PublicKey, pk.MLKEM768PublicKey, fp)
	var out NoiseExecutorRegistration
	if err := row.Scan(
		&out.RegistrationID, &out.EnvID,
		&out.PublicKey.Suite, &out.PublicKey.X25519PublicKey, &out.PublicKey.MLKEM768PublicKey,
		&out.CreatedAt, &out.LastSeenAt,
	); err != nil {
		return NoiseExecutorRegistration{}, fmt.Errorf("upsert noise registration: %w", err)
	}
	return out, nil
}

// LookupNoiseExecutorRegistration fetches a registration by ID. Used
// by /connect to retrieve the executor's pubkey to hand to the harness.
func (s *Store) LookupNoiseExecutorRegistration(ctx context.Context, registrationID string) (NoiseExecutorRegistration, error) {
	const q = `
		SELECT registration_id, env_id, suite, x25519_public_key, mlkem768_public_key, created_at, last_seen_at
		FROM noise_executor_registrations
		WHERE registration_id = $1
	`
	row := s.db.QueryRowContext(ctx, q, registrationID)
	var out NoiseExecutorRegistration
	if err := row.Scan(
		&out.RegistrationID, &out.EnvID,
		&out.PublicKey.Suite, &out.PublicKey.X25519PublicKey, &out.PublicKey.MLKEM768PublicKey,
		&out.CreatedAt, &out.LastSeenAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NoiseExecutorRegistration{}, ErrNoiseRegistrationNotFound
		}
		return NoiseExecutorRegistration{}, fmt.Errorf("lookup noise registration: %w", err)
	}
	return out, nil
}

// LookupNoiseExecutorRegistrationByEnv returns the most recently seen
// registration for the given env_id. Used by /connect when the harness
// addresses an env_id rather than a specific registration_id.
func (s *Store) LookupNoiseExecutorRegistrationByEnv(ctx context.Context, envID string) (NoiseExecutorRegistration, error) {
	const q = `
		SELECT registration_id, env_id, suite, x25519_public_key, mlkem768_public_key, created_at, last_seen_at
		FROM noise_executor_registrations
		WHERE env_id = $1
		ORDER BY last_seen_at DESC
		LIMIT 1
	`
	row := s.db.QueryRowContext(ctx, q, envID)
	var out NoiseExecutorRegistration
	if err := row.Scan(
		&out.RegistrationID, &out.EnvID,
		&out.PublicKey.Suite, &out.PublicKey.X25519PublicKey, &out.PublicKey.MLKEM768PublicKey,
		&out.CreatedAt, &out.LastSeenAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NoiseExecutorRegistration{}, ErrNoiseRegistrationNotFound
		}
		return NoiseExecutorRegistration{}, fmt.Errorf("lookup noise registration by env: %w", err)
	}
	return out, nil
}

// TouchNoiseExecutorRegistration bumps last_seen_at without changing
// pubkey columns. Called from the relay WS handler on each successful
// keepalive ping so the eviction sweep can distinguish live executors
// from stale rows.
func (s *Store) TouchNoiseExecutorRegistration(ctx context.Context, registrationID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE noise_executor_registrations SET last_seen_at = NOW() WHERE registration_id = $1`,
		registrationID,
	)
	if err != nil {
		return fmt.Errorf("touch noise registration: %w", err)
	}
	return nil
}

// ErrNoiseRegistrationNotFound is returned by lookup methods when no
// row matches. Handlers translate to HTTP 404.
var ErrNoiseRegistrationNotFound = errors.New("noise executor registration not found")

func pubkeyFingerprint(pk noise.PublicKey) []byte {
	h := sha256.New()
	// Length-prefix each component so distinct (suite, dh, kem) tuples
	// can never collide via concatenation games.
	for _, part := range [][]byte{[]byte(pk.Suite), []byte(pk.X25519PublicKey), []byte(pk.MLKEM768PublicKey)} {
		var lenBuf [8]byte
		// big-endian for consistency with the noise prologue framing
		for i := 7; i >= 0; i-- {
			lenBuf[i] = byte(len(part) >> uint(8*(7-i)))
		}
		h.Write(lenBuf[:])
		h.Write(part)
	}
	sum := h.Sum(nil)
	return sum
}
