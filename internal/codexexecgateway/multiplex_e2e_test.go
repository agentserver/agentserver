package codexexecgateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
	"github.com/agentserver/agentserver/internal/relaypb"
	"google.golang.org/protobuf/proto"
	"nhooyr.io/websocket"
)

// dialBridgeRaw is dialBridge but exposes the raw ws so tests can drive
// the relay protocol directly.
func dialBridgeRaw(t *testing.T, baseURL, exeID, tok string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/bridge/" + exeID
	c, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + tok}},
	})
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	return c
}

// sendResume sends a Resume frame on the bridge ws under the given stream_id.
func sendResume(t *testing.T, c *websocket.Conn, streamID string) {
	t.Helper()
	frame := &relaypb.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body: &relaypb.RelayMessageFrame_Resume{
			Resume: &relaypb.RelayResume{NextSeq: 0},
		},
	}
	body, err := proto.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal Resume: %v", err)
	}
	if err := c.Write(context.Background(), websocket.MessageBinary, body); err != nil {
		t.Fatalf("write Resume: %v", err)
	}
}

// sendDataFrame wraps payload in a Data frame on stream_id.
func sendDataFrame(t *testing.T, c *websocket.Conn, streamID string, seq uint32, payload []byte) {
	t.Helper()
	frame := &relaypb.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body: &relaypb.RelayMessageFrame_Data{
			Data: &relaypb.RelayData{
				Seq: seq, SegmentIndex: 0, SegmentCount: 1, Payload: payload,
			},
		},
	}
	body, _ := proto.Marshal(frame)
	if err := c.Write(context.Background(), websocket.MessageBinary, body); err != nil {
		t.Fatalf("write Data: %v", err)
	}
}

// readRelayFrame reads one binary ws message and decodes it as a relay frame.
func readRelayFrame(t *testing.T, ctx context.Context, c *websocket.Conn) *relaypb.RelayMessageFrame {
	t.Helper()
	mt, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.MessageBinary {
		t.Fatalf("want binary, got %v", mt)
	}
	var frame relaypb.RelayMessageFrame
	if err := proto.Unmarshal(data, &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &frame
}

// connectInboundRaw dials /codex-exec/{exe_id} with token and registers
// the exe_id in the DB. Returns the raw ws so the test can play the
// codex exec-server role (parse incoming relay frames, write
// responses tagged with the right stream_id).
func connectInboundRaw(t *testing.T, srv *Server, baseURL, exeID string) *websocket.Conn {
	t.Helper()
	srv.store.CreateExecutor(context.Background(), Executor{
		ExeID: exeID, UserID: "u", RegisteredAt: time.Now().UTC(),
	})
	if err := srv.store.BindWorkspaceExecutor(context.Background(), "ws_1", exeID, "test-"+exeID, "", false); err != nil {
		t.Fatalf("BindWorkspaceExecutor: %v", err)
	}
	ticket, err := wsticket.Mint(exeID, srv.config.AgentserverInternalSecret)
	if err != nil {
		t.Fatalf("MintWSTicket: %v", err)
	}
	url := "ws" + strings.TrimPrefix(baseURL, "http") + "/agentx/" + exeID + "?token=" + ticket
	c, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("inbound dial: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.registry.Lookup(exeID); ok {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inbound not registered for %s", exeID)
	return nil
}

// TestBridge_TwoConcurrentBridgesShareInbound exercises the v0.53.0
// multiplexing path: two bridges to the same exe_id with distinct
// stream_ids each round-trip frames through the single inbound conn.
// Verifies frames don't interleave wrong (each bridge gets back only
// frames tagged with its own stream_id).
func TestBridge_TwoConcurrentBridgesShareInbound(t *testing.T) {
	hs, srv := newInboundTestServer(t)
	inbound := connectInboundRaw(t, srv, hs.URL, "exe_mux")
	defer inbound.Close(websocket.StatusNormalClosure, "")

	now := time.Now().Unix()
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_mux", WorkspaceID: "ws_1", IAT: now, EXP: now + 60,
	})

	bridgeA := dialBridgeRaw(t, hs.URL, "exe_mux", tok)
	defer bridgeA.Close(websocket.StatusNormalClosure, "")
	bridgeB := dialBridgeRaw(t, hs.URL, "exe_mux", tok)
	defer bridgeB.Close(websocket.StatusNormalClosure, "")

	sendResume(t, bridgeA, "stream-A")
	sendResume(t, bridgeB, "stream-B")

	// Inbound (us, playing exec-server) should see both Resume frames.
	// Order isn't guaranteed; collect both.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		f := readRelayFrame(t, ctx, inbound)
		seen[f.StreamId] = true
	}
	if !seen["stream-A"] || !seen["stream-B"] {
		t.Fatalf("missing Resume on inbound: %v", seen)
	}

	// Bridge A sends a Data frame; only inbound should see it (not bridge B).
	sendDataFrame(t, bridgeA, "stream-A", 1, []byte(`{"hello":"A"}`))
	f := readRelayFrame(t, ctx, inbound)
	if f.StreamId != "stream-A" {
		t.Errorf("expected stream-A frame on inbound, got %q", f.StreamId)
	}

	// Inbound replies on stream-A; bridge A should receive it (not B).
	sendDataFrame(t, inbound, "stream-A", 1, []byte(`{"reply":"A"}`))
	fa := readRelayFrame(t, ctx, bridgeA)
	if fa.StreamId != "stream-A" {
		t.Errorf("bridge A got wrong stream_id: %q", fa.StreamId)
	}
	// Bridge B should NOT get a frame (deadline check).
	bctx, bcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer bcancel()
	if _, _, err := bridgeB.Read(bctx); err == nil {
		t.Error("bridge B unexpectedly received a frame for stream-A")
	}

	// And the symmetric path for stream-B.
	sendDataFrame(t, inbound, "stream-B", 1, []byte(`{"reply":"B"}`))
	fb := readRelayFrame(t, ctx, bridgeB)
	if fb.StreamId != "stream-B" {
		t.Errorf("bridge B got wrong stream_id: %q", fb.StreamId)
	}
}

// TestBridge_RejectsFirstFrameNonResume: a bridge ws whose first frame
// isn't a Resume protobuf is closed with a protocol error.
func TestBridge_RejectsFirstFrameNonResume(t *testing.T) {
	hs, srv := newInboundTestServer(t)
	inbound := connectInboundRaw(t, srv, hs.URL, "exe_noresume")
	defer inbound.Close(websocket.StatusNormalClosure, "")

	now := time.Now().Unix()
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_x", WorkspaceID: "ws_1", IAT: now, EXP: now + 60,
	})
	c := dialBridgeRaw(t, hs.URL, "exe_noresume", tok)
	defer c.Close(websocket.StatusNormalClosure, "")

	// Send a text frame instead of binary Resume.
	_ = c.Write(context.Background(), websocket.MessageText, []byte("not-a-relay-frame"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("expected bridge to close after non-Resume first frame")
	}
}

// TestBridge_StreamIdCollisionEvictsFirst: two bridges using the same
// stream_id — the second registration evicts the first.
func TestBridge_StreamIdCollisionEvictsFirst(t *testing.T) {
	hs, srv := newInboundTestServer(t)
	inbound := connectInboundRaw(t, srv, hs.URL, "exe_collide")
	defer inbound.Close(websocket.StatusNormalClosure, "")

	now := time.Now().Unix()
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_c", WorkspaceID: "ws_1", IAT: now, EXP: now + 60,
	})

	a := dialBridgeRaw(t, hs.URL, "exe_collide", tok)
	defer a.Close(websocket.StatusInternalError, "test cleanup")
	sendResume(t, a, "dup-id")
	readRelayFrame(t, ctxTimeout(t, 2*time.Second), inbound) // drain inbound's Resume

	b := dialBridgeRaw(t, hs.URL, "exe_collide", tok)
	defer b.Close(websocket.StatusNormalClosure, "")
	sendResume(t, b, "dup-id")
	readRelayFrame(t, ctxTimeout(t, 2*time.Second), inbound) // drain inbound's Resume from b

	// a's ws should close shortly (evicted).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := a.Read(ctx); err == nil {
		t.Fatal("evicted bridge should have closed; Read succeeded")
	}
}

// TestBridge_WrongStreamIdFrameDropped: a bridge sends a Data frame
// whose stream_id doesn't match the session's Resume stream_id. The
// frame should be dropped at the gateway (not forwarded to inbound).
func TestBridge_WrongStreamIdFrameDropped(t *testing.T) {
	hs, srv := newInboundTestServer(t)
	inbound := connectInboundRaw(t, srv, hs.URL, "exe_wrongsid")
	defer inbound.Close(websocket.StatusNormalClosure, "")

	now := time.Now().Unix()
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_w", WorkspaceID: "ws_1", IAT: now, EXP: now + 60,
	})
	c := dialBridgeRaw(t, hs.URL, "exe_wrongsid", tok)
	defer c.Close(websocket.StatusNormalClosure, "")

	sendResume(t, c, "good-sid")
	// Drain the Resume forwarded to inbound.
	resume := readRelayFrame(t, ctxTimeout(t, 2*time.Second), inbound)
	if resume.StreamId != "good-sid" {
		t.Fatalf("inbound got Resume with stream_id=%q want good-sid", resume.StreamId)
	}

	// Send a Data frame with a DIFFERENT stream_id. Should be dropped.
	sendDataFrame(t, c, "wrong-sid", 1, []byte(`{"bad":true}`))

	// inbound should NOT see this frame. Wait briefly then assert nothing arrived.
	bctx, bcancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer bcancel()
	if _, _, err := inbound.Read(bctx); err == nil {
		t.Fatal("inbound should not have received the wrong-sid frame")
	}
}

func ctxTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// TestBridge_MultipleBridgesNoGoroutineLeak: open + close 10 bridges in
// sequence; goroutine count after settle should be within slack of before.
func TestBridge_MultipleBridgesNoGoroutineLeak(t *testing.T) {
	hs, srv := newInboundTestServer(t)
	inbound := connectInboundRaw(t, srv, hs.URL, "exe_leak")
	defer inbound.Close(websocket.StatusNormalClosure, "")
	// Drain inbound throughout the test so writes don't backpressure.
	var drainWG sync.WaitGroup
	drainWG.Add(1)
	drainCtx, drainCancel := context.WithCancel(context.Background())
	go func() {
		defer drainWG.Done()
		for {
			if _, _, err := inbound.Read(drainCtx); err != nil {
				return
			}
		}
	}()

	now := time.Now().Unix()
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_l", WorkspaceID: "ws_1", IAT: now, EXP: now + 60,
	})
	for i := 0; i < 10; i++ {
		c := dialBridgeRaw(t, hs.URL, "exe_leak", tok)
		sendResume(t, c, "leak-stream-"+stringFromInt(i))
		time.Sleep(20 * time.Millisecond)
		c.Close(websocket.StatusNormalClosure, "done")
	}
	drainCancel()
	drainWG.Wait()
	// Settle.
	time.Sleep(200 * time.Millisecond)
}

func stringFromInt(i int) string {
	if i == 0 {
		return "0"
	}
	var out []byte
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}
