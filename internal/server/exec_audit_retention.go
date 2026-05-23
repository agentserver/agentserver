package server

import (
	"context"
	"log"
	"time"
)

type AuditRetentionResult struct {
	Sessions int64
	Calls    int64
	Payloads int64
}

// StartAuditRetentionLoop prunes exec_audit_* rows older than ttl, once
// per tick. ttl <= 0 disables (loop never starts).
func (s *Server) StartAuditRetentionLoop(ctx context.Context, ttl, tick time.Duration) {
	if ttl <= 0 {
		log.Printf("exec-audit retention: disabled (ttl=0)")
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cutoff := now.UTC().Add(-ttl)
			res, err := s.runAuditRetentionOnce(ctx, cutoff)
			if err != nil {
				log.Printf("exec-audit retention: prune failed: %v", err)
				continue
			}
			log.Printf("exec-audit retention: pruned sessions=%d calls=%d payloads=%d cutoff=%s",
				res.Sessions, res.Calls, res.Payloads, cutoff.Format(time.RFC3339))
		}
	}
}

func (s *Server) runAuditRetentionOnce(_ context.Context, cutoff time.Time) (AuditRetentionResult, error) {
	sess, calls, payloads, err := s.DB.PruneAuditOlderThan(cutoff)
	return AuditRetentionResult{Sessions: sess, Calls: calls, Payloads: payloads}, err
}

// RunAuditRetentionOnce is exported for tests.
func RunAuditRetentionOnce(ctx context.Context, s *Server, cutoff time.Time) (AuditRetentionResult, error) {
	return s.runAuditRetentionOnce(ctx, cutoff)
}
