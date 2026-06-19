package codexexecgateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"nhooyr.io/websocket"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	"github.com/go-chi/chi/v5"
	relayv1 "github.com/agentserver/agentserver/internal/relaypb"
)

// TestNoiseRouter_LoopbackHandshakeAndEcho stands up a real gateway
// HTTP/WS server backed by the actual NoiseHandlers + NoiseRouter,
// then plays the executor side in-process using the noise package's
// responder API. Verifies the full Phase 3 path: register → executor
// WS connect → router demux → OpenStream initiator handshake →
// bidirectional plaintext piping through the encrypted channel.
func TestNoiseRouter_LoopbackHandshakeAndEcho(t *testing.T) {
	store := newTestStore(t)

	gwIdentity, err := noise.GenerateIdentity()
	if err != nil {
		t.Fatalf("gw identity: %v", err)
	}
	hmacKey := mustRandKey(t, 32)

	handlers := NewNoiseHandlers(store, hmacKey, "")
	router := NewNoiseRouter(store, handlers.WSHub(), gwIdentity, hmacKey)
	handlers.AttachRouter(router)

	r := chi.NewRouter()
	handlers.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	const envID = "router-loopback-env"

	// 1. Generate executor identity and register against the gateway.
	execIdentity, err := noise.GenerateIdentity()
	if err != nil {
		t.Fatalf("exec identity: %v", err)
	}
	reg, err := postNoiseRegister(t, srv.URL, envID, execIdentity.PublicKey())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// 2. Open the executor WS to the gateway. Run a goroutine acting
	//    as the executor responder side: read frames, run the noise
	//    responder per stream, echo plaintext back.
	execCtx, execCancel := context.WithCancel(context.Background())
	defer execCancel()
	execConn, _, err := websocket.Dial(execCtx, toWS(reg.URL), nil)
	if err != nil {
		t.Fatalf("executor ws dial: %v", err)
	}
	defer execConn.Close(websocket.StatusNormalClosure, "test end")
	execConn.SetReadLimit(256 * 1024)

	exec := &fakeExecutor{
		conn:           execConn,
		identity:       execIdentity,
		envID:          envID,
		registrationID: reg.ExecutorRegistrationID,
		gwURL:          srv.URL,
		streams:        map[string]*noise.Transport{},
	}
	execDone := make(chan struct{})
	go func() {
		exec.serve(execCtx, t)
		close(execDone)
	}()

	// Give the executor WS time to land in the hub before opening.
	for i := 0; i < 50 && handlers.WSHub().ConnectionFor(reg.ExecutorRegistrationID) == nil; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if handlers.WSHub().ConnectionFor(reg.ExecutorRegistrationID) == nil {
		t.Fatalf("executor WS never landed in hub")
	}

	// 3. Open a noise stream from gateway → executor. The harness side
	//    is a net.Pipe — we write to one end and expect to read the
	//    echo back from the other end.
	harnessRouter, harnessApp := newDuplexPipe()
	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer openCancel()
	openErr := make(chan error, 1)
	go func() { openErr <- router.OpenStream(openCtx, envID, harnessRouter) }()

	want := []byte("ping noise hybrid IK echo test\n")
	if _, err := harnessApp.Write(want); err != nil {
		t.Fatalf("harness write: %v", err)
	}
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(harnessApp, buf); err != nil {
		t.Fatalf("harness read: %v", err)
	}
	if string(buf) != string(want) {
		t.Errorf("echo mismatch\n got=%q\nwant=%q", buf, want)
	}

	// Second round-trip on the same stream — proves nonce monotonicity.
	want2 := []byte("second round\n")
	_, _ = harnessApp.Write(want2)
	buf2 := make([]byte, len(want2))
	if _, err := io.ReadFull(harnessApp, buf2); err != nil {
		t.Fatalf("harness read 2: %v", err)
	}
	if string(buf2) != string(want2) {
		t.Errorf("echo 2 mismatch")
	}

	// Tear down the harness; OpenStream should return.
	_ = harnessApp.Close()
	_ = harnessRouter.Close()
	select {
	case <-openErr:
	case <-time.After(2 * time.Second):
		t.Fatalf("OpenStream did not return after harness close")
	}

	execCancel()
	<-execDone
}

// fakeExecutor is an in-process test responder that plays the codex
// exec-server role: reads RelayMessageFrame from the WS, runs the
// noise package's PendingResponderHandshake against msg1, sends msg2
// back, then echoes plaintext on data frames.
type fakeExecutor struct {
	conn            *websocket.Conn
	identity        *noise.Identity
	envID           string
	registrationID  string
	gwURL           string
	mu              sync.Mutex
	streams         map[string]*noise.Transport
	nextSendSeq     map[string]uint32
}

func (e *fakeExecutor) serve(ctx context.Context, t *testing.T) {
	for {
		mt, payload, err := e.conn.Read(ctx)
		if err != nil {
			return
		}
		if mt != websocket.MessageBinary {
			continue
		}
		var frame relayv1.RelayMessageFrame
		if err := proto.Unmarshal(payload, &frame); err != nil {
			t.Errorf("exec unmarshal: %v", err)
			return
		}
		switch body := frame.Body.(type) {
		case *relayv1.RelayMessageFrame_Handshake:
			e.handleHandshake(ctx, t, frame.StreamId, body.Handshake.Payload)
		case *relayv1.RelayMessageFrame_Data:
			e.handleData(ctx, t, frame.StreamId, body.Data)
		case *relayv1.RelayMessageFrame_Reset_:
			e.mu.Lock()
			delete(e.streams, frame.StreamId)
			e.mu.Unlock()
		}
	}
}

func (e *fakeExecutor) handleHandshake(ctx context.Context, t *testing.T, streamID string, msg1 []byte) {
	prologue := noise.Prologue(e.envID, e.registrationID, streamID)
	pending, err := noise.ReadInitiatorRequest(e.identity, prologue, msg1)
	if err != nil {
		t.Errorf("exec read msg1: %v", err)
		return
	}
	// We could POST /validate here to mirror real codex, but for the
	// loopback test we trust the authorization (the gateway minted it
	// from the same HMAC key NoiseHandlers uses, so /validate would
	// just return true — verified by TestNoiseValidate_RoundTrip).
	transport, msg2, err := pending.Complete()
	if err != nil {
		t.Errorf("exec complete: %v", err)
		return
	}
	e.mu.Lock()
	e.streams[streamID] = transport
	if e.nextSendSeq == nil {
		e.nextSendSeq = map[string]uint32{}
	}
	e.mu.Unlock()

	hsFrame := &relayv1.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body:     &relayv1.RelayMessageFrame_Handshake{Handshake: &relayv1.RelayHandshake{Payload: msg2}},
	}
	out, _ := proto.Marshal(hsFrame)
	if err := e.conn.Write(ctx, websocket.MessageBinary, out); err != nil {
		t.Errorf("exec write msg2: %v", err)
	}
}

func (e *fakeExecutor) handleData(ctx context.Context, t *testing.T, streamID string, data *relayv1.RelayData) {
	e.mu.Lock()
	transport := e.streams[streamID]
	e.mu.Unlock()
	if transport == nil {
		return
	}
	pt, err := transport.Decrypt(data.Payload)
	if err != nil {
		t.Errorf("exec decrypt: %v", err)
		return
	}
	// Echo plaintext back.
	ct, err := transport.Encrypt(pt)
	if err != nil {
		t.Errorf("exec encrypt: %v", err)
		return
	}
	e.mu.Lock()
	seq := e.nextSendSeq[streamID]
	e.nextSendSeq[streamID] = seq + 1
	e.mu.Unlock()
	frame := &relayv1.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body: &relayv1.RelayMessageFrame_Data{
			Data: &relayv1.RelayData{
				Seq:          seq,
				SegmentIndex: 0,
				SegmentCount: 1,
				Payload:      ct,
			},
		},
	}
	out, _ := proto.Marshal(frame)
	_ = e.conn.Write(ctx, websocket.MessageBinary, out)
}

// --- small helpers

func postNoiseRegister(t *testing.T, baseURL, envID string, pk noise.PublicKey) (noiseRegisterResponse, error) {
	t.Helper()
	body, _ := json.Marshal(noiseRegisterRequest{
		SecurityProfile:   NoiseRelaySecurityProfile,
		ExecutorPublicKey: pk,
	})
	resp, err := http.Post(baseURL+"/cloud/environment/"+envID+"/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return noiseRegisterResponse{}, err
	}
	defer resp.Body.Close()
	var out noiseRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return noiseRegisterResponse{}, err
	}
	return out, nil
}

func toWS(u string) string {
	parsed, _ := url.Parse(u)
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	return parsed.String()
}

func mustRandKey(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

// newDuplexPipe returns two ends of an in-memory bidirectional byte
// pipe (analogous to net.Pipe but using channel-backed io.Pipe pairs
// so closes propagate cleanly).
func newDuplexPipe() (a, b io.ReadWriteCloser) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	return &pipeRW{r: ar, w: aw}, &pipeRW{r: br, w: bw}
}

type pipeRW struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRW) Close() error {
	_ = p.r.Close()
	_ = p.w.Close()
	return nil
}

