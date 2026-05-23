package processes

import (
	"sync"
	"time"
)

// MaxBufferBytes is the per-session cap on accumulated stdout+stderr.
// Anything beyond this is truncated from the head of the ring; the
// session records how many bytes were lost so the SDK can warn.
const MaxBufferBytes = 1 << 20 // 1 MiB

// Chunk is one stdout or stderr segment delivered to the SDK. Seq is
// monotonically increasing per-session starting at 1; the SDK passes
// the highest Seq it has seen as the `since` query param to get only
// newer chunks.
type Chunk struct {
	Stream string `json:"stream"` // "stdout" or "stderr"
	Data   []byte `json:"-"`
	Seq    int    `json:"seq"`
}

// Session is one long-running process spawned via tools.UnifiedExec /
// exec_command. The SDK polls Output, writes stdin, and terminates via
// the corresponding /api/connectors/processes/{sid}/* endpoints.
type Session struct {
	ID          string
	WorkspaceID string
	mu           sync.Mutex
	chunks       []Chunk
	seq          int
	bytesBuf     int
	lostBytes    int
	exitCode     *int
	lastActivity time.Time
}

// Append adds a chunk and updates lastActivity. Old chunks are dropped
// from the head when the total exceeds MaxBufferBytes; the dropped
// bytes are accumulated into lostBytes so the SDK can warn.
func (s *Session) Append(stream string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.chunks = append(s.chunks, Chunk{Stream: stream, Data: append([]byte(nil), data...), Seq: s.seq})
	s.bytesBuf += len(data)
	s.lastActivity = time.Now()
	for s.bytesBuf > MaxBufferBytes && len(s.chunks) > 1 {
		drop := s.chunks[0]
		s.chunks = s.chunks[1:]
		s.bytesBuf -= len(drop.Data)
		s.lostBytes += len(drop.Data)
	}
}

// OutputSince returns every chunk whose Seq > since, plus the current
// exit code (nil if still running) and an alive flag.
func (s *Session) OutputSince(since int) (chunks []Chunk, exit *int, alive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.chunks {
		if c.Seq > since {
			chunks = append(chunks, c)
		}
	}
	alive = s.exitCode == nil
	exit = s.exitCode
	return
}

// LostBytes is the total bytes dropped from the head of the ring after
// the buffer hit MaxBufferBytes. Reset to 0 on a fresh session.
func (s *Session) LostBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lostBytes
}

// SetExit records the process exit code (or signal) and refreshes
// lastActivity so the session lives one more idle-timeout window for
// final output polling.
func (s *Session) SetExit(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exitCode = &code
	s.lastActivity = time.Now()
}

// LastActivity is the time of the most recent Append / SetExit.
func (s *Session) LastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivity
}
