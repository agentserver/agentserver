package agentxconn

import (
	"errors"
	"sync"
	"time"
)

// RegistryConfig contains only process-local journal limits. The generation
// passed to Attach must already have been acquired and committed through core;
// Registry is a second fence, not a database authority.
type RegistryConfig struct {
	WireLimits              Limits
	MaxUnackedFrames        int
	MaxJournalBytes         int
	MaxReceiveHistoryFrames int
	ResumeWindow            time.Duration
}

type Registry struct {
	mu sync.Mutex

	gatewayInstanceID string
	config            RegistryConfig
	byExecutor        map[string]*Session
	bySession         map[string]*Session
}

func NewRegistry(gatewayInstanceID string, config RegistryConfig) (*Registry, error) {
	if err := validateUUID("gatewayInstanceId", gatewayInstanceID); err != nil {
		return nil, err
	}
	probe, err := NewSession(SessionConfig{
		Role:                    RoleGateway,
		GatewayInstanceID:       gatewayInstanceID,
		ExecutorID:              "10000000-0000-0000-0000-000000000001",
		SessionID:               "20000000-0000-0000-0000-000000000002",
		Generation:              1,
		WireLimits:              config.WireLimits,
		MaxUnackedFrames:        config.MaxUnackedFrames,
		MaxJournalBytes:         config.MaxJournalBytes,
		MaxReceiveHistoryFrames: config.MaxReceiveHistoryFrames,
		ResumeWindow:            config.ResumeWindow,
	})
	if err != nil {
		return nil, err
	}
	probe.Close(errors.New("registry configuration probe complete"))
	return &Registry{
		gatewayInstanceID: gatewayInstanceID,
		config:            config,
		byExecutor:        make(map[string]*Session),
		bySession:         make(map[string]*Session),
	}, nil
}

// Attach installs a fresh, core-authorized connection generation. A resume of
// an existing generation must use Resume instead; accepting it here would
// silently discard the old frame journal.
func (r *Registry) Attach(executorID, sessionID string, generation int64) (*Session, error) {
	if err := validateUUID("executorId", executorID); err != nil {
		return nil, err
	}
	if err := validateUUID("sessionId", sessionID); err != nil {
		return nil, err
	}
	if generation < 1 {
		return nil, errors.New("connection generation must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, duplicate := r.bySession[sessionID]; duplicate {
		return nil, protocolError(ErrorSessionMismatch, true, "sessionId %q is already registered", sessionID)
	}
	prior := r.byExecutor[executorID]
	if prior != nil {
		priorSnapshot := prior.Snapshot()
		priorGeneration := prior.config.Generation
		if generation <= priorGeneration {
			return nil, protocolError(ErrorStaleGeneration, true, "new generation %d does not exceed current generation %d in state %s", generation, priorGeneration, priorSnapshot.State)
		}
	}
	session, err := NewSession(SessionConfig{
		Role:                    RoleGateway,
		GatewayInstanceID:       r.gatewayInstanceID,
		ExecutorID:              executorID,
		SessionID:               sessionID,
		Generation:              generation,
		WireLimits:              r.config.WireLimits,
		MaxUnackedFrames:        r.config.MaxUnackedFrames,
		MaxJournalBytes:         r.config.MaxJournalBytes,
		MaxReceiveHistoryFrames: r.config.MaxReceiveHistoryFrames,
		ResumeWindow:            r.config.ResumeWindow,
	})
	if err != nil {
		return nil, err
	}
	if prior != nil {
		prior.Fence(generation)
		delete(r.bySession, prior.config.SessionID)
	}
	r.byExecutor[executorID] = session
	r.bySession[sessionID] = session
	return session, nil
}

func (r *Registry) Disconnect(sessionID string, now time.Time) error {
	r.mu.Lock()
	session := r.bySession[sessionID]
	r.mu.Unlock()
	if session == nil {
		return protocolError(ErrorResumeRejected, true, "session is not owned by this gateway process")
	}
	return session.Disconnect(now)
}

func (r *Registry) Resume(executorID string, cursor ResumeCursor, now time.Time) (*Session, ResumeResult, error) {
	if cursor.GatewayInstanceID != r.gatewayInstanceID {
		return nil, ResumeResult{}, protocolError(ErrorResumeRejected, true, "resume names a different gateway process")
	}
	r.mu.Lock()
	session := r.bySession[cursor.SessionID]
	owner := r.byExecutor[executorID]
	if session == nil || owner != session {
		r.mu.Unlock()
		return nil, ResumeResult{}, protocolError(ErrorResumeRejected, true, "resume session is not the current executor owner")
	}
	result, err := session.Resume(ResumeRequest{
		GatewayInstanceID:   cursor.GatewayInstanceID,
		SessionID:           cursor.SessionID,
		Generation:          cursor.Generation,
		PeerSentThrough:     cursor.AgentxSentThrough,
		PeerReceivedThrough: cursor.AgentxReceivedThrough,
	}, now)
	r.mu.Unlock()
	if err != nil {
		return nil, ResumeResult{}, err
	}
	return session, result, nil
}

func (r *Registry) Current(executorID string) (*Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, found := r.byExecutor[executorID]
	return session, found
}
