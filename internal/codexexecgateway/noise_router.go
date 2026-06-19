package codexexecgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"nhooyr.io/websocket"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	relayv1 "github.com/agentserver/agentserver/internal/relaypb"
)

// NoiseRouter is the gateway's noise initiator + per-executor frame
// demultiplexer. Sits between the harness side (bridge.go) and the
// executor side (the noiseWSHub WS connections), opening per-stream
// hybrid IK handshakes on demand and pumping plaintext bytes in/out
// while the wire stays AES-256-GCM encrypted.
//
// One NoiseRouter per gateway process. Streams are scoped by
// (registration_id, stream_id) and live in the streams map until the
// harness disconnects or the executor sends RelayReset.
type NoiseRouter struct {
	store    *Store
	hub      *noiseWSHub
	identity *noise.Identity // gateway harness identity, persistent for the process
	hmacKey  []byte          // for minting harness_key_authorization

	mu      sync.Mutex
	streams map[streamKey]*activeStream
}

type streamKey struct {
	registrationID string
	streamID       string
}

// activeStream couples per-stream noise state with the channels the
// demux loop uses to deliver incoming frames.
type activeStream struct {
	registrationID string
	streamID       string

	// During handshake: pendingHS is non-nil and inbound msg2 frames
	// arrive on handshakeCh. After handshake completes pendingHS is
	// cleared and transport takes over.
	pendingHS   *noise.InitiatorHandshake
	handshakeCh chan []byte // size 1, msg2 lands here

	// transport is set after Finish(). dataCh delivers decrypted
	// payloads (in order) to the harness pump.
	transport *noise.Transport
	dataCh    chan []byte // unbounded — pump drains promptly

	// nextSendSeq tracks the relay seq we'll stamp on the NEXT
	// outbound RelayData. Per codex spec this is monotonic per
	// (stream_id, direction). 32-bit per RelayData.seq protobuf field.
	nextSendSeq uint32

	// closed is closed once the stream is torn down; both pumps
	// observe it and exit.
	closed chan struct{}
	once   sync.Once
}

func (s *activeStream) shutdown() {
	s.once.Do(func() { close(s.closed) })
}

// NewNoiseRouter constructs a NoiseRouter using the given gateway
// identity. The HMAC key must match the one NoiseHandlers uses to
// mint harness authorizations — otherwise the executor's POST
// /validate against the gateway would fail.
func NewNoiseRouter(store *Store, hub *noiseWSHub, identity *noise.Identity, hmacKey []byte) *NoiseRouter {
	return &NoiseRouter{
		store:    store,
		hub:      hub,
		identity: identity,
		hmacKey:  hmacKey,
		streams:  map[streamKey]*activeStream{},
	}
}

// GatewayPublicKey is the harness static pubkey the gateway will pin
// in /connect responses. Tests + the relay handler use this.
func (r *NoiseRouter) GatewayPublicKey() noise.PublicKey {
	return r.identity.PublicKey()
}

// ServeExecutorFrames is the demux loop for one executor WS
// connection. The wsHub.accept path delegates to this once an
// executor's WS is registered. The loop reads RelayMessageFrame
// proto messages and routes Handshake/Data/Reset frames to the
// matching activeStream.
//
// Returns nil on clean WS close, an error on protocol violations
// (which propagates as a WS close from the caller).
func (r *NoiseRouter) ServeExecutorFrames(ctx context.Context, registrationID string, conn *websocket.Conn) error {
	for {
		mt, payload, err := conn.Read(ctx)
		if err != nil {
			return nil // peer closed; ctx cancelled
		}
		if mt != websocket.MessageBinary {
			// codex only sends binary frames over the noise relay WS;
			// text is a protocol error.
			continue
		}
		var frame relayv1.RelayMessageFrame
		if err := proto.Unmarshal(payload, &frame); err != nil {
			// One malformed frame doesn't justify killing the WS —
			// codex's run_multiplexed_environment also drops and
			// continues.
			continue
		}
		key := streamKey{registrationID: registrationID, streamID: frame.StreamId}
		r.mu.Lock()
		stream := r.streams[key]
		r.mu.Unlock()
		if stream == nil {
			// Stream not active here — e.g. our side reset it but the
			// peer hasn't seen the reset yet. Drop quietly.
			continue
		}
		switch body := frame.Body.(type) {
		case *relayv1.RelayMessageFrame_Handshake:
			// Deliver msg2 to OpenStream goroutine waiting on
			// handshakeCh. Non-blocking — if no one is waiting the
			// handshake already timed out and the channel is gone.
			select {
			case stream.handshakeCh <- body.Handshake.Payload:
			case <-stream.closed:
			default:
			}
		case *relayv1.RelayMessageFrame_Data:
			if stream.transport == nil {
				// Data before handshake completion → drop.
				continue
			}
			pt, err := stream.transport.Decrypt(body.Data.Payload)
			if err != nil {
				// AEAD failure or out-of-order → reset the stream.
				r.resetStream(ctx, conn, stream, "decrypt_failed")
				continue
			}
			select {
			case stream.dataCh <- pt:
			case <-stream.closed:
				return nil
			case <-ctx.Done():
				return nil
			}
		case *relayv1.RelayMessageFrame_Reset_:
			fmt.Fprintf(noiseRouterDebug, "noise router: demux Reset stream=%s reason=%q\n",
				frame.StreamId, body.Reset_.Reason)
			r.dropStream(key)
			stream.shutdown()
		case *relayv1.RelayMessageFrame_Heartbeat:
			// no-op
		default:
			// Acks / Resume / other body kinds are ignored for v1.
		}
	}
}

// OpenStream initiates a noise hybrid IK handshake for envID, then
// runs bidirectional plaintext piping between `harness` and the
// executor WS until either side closes or ctx is cancelled.
//
// Returns once both pumps exit (i.e. the stream lifecycle is over).
// Caller treats this like an io.Copy — block, then close `harness`.
func (r *NoiseRouter) OpenStream(ctx context.Context, envID string, harness io.ReadWriteCloser) error {
	reg, err := r.store.LookupNoiseExecutorRegistrationByEnv(ctx, envID)
	if err != nil {
		return fmt.Errorf("noise router: no executor for env %q: %w", envID, err)
	}
	conn := r.hub.ConnectionFor(reg.RegistrationID)
	if conn == nil {
		return fmt.Errorf("noise router: executor %q not currently connected", reg.RegistrationID)
	}

	streamID := "str_" + uuid.NewString()
	auth := mintHarnessAuthorization(r.hmacKey, reg.RegistrationID, r.identity.PublicKey())
	prologue := noise.Prologue(envID, reg.RegistrationID, streamID)
	hs, msg1, err := noise.StartInitiator(r.identity, reg.PublicKey, prologue, []byte(auth))
	if err != nil {
		return fmt.Errorf("noise router: start initiator: %w", err)
	}

	stream := &activeStream{
		registrationID: reg.RegistrationID,
		streamID:       streamID,
		pendingHS:      hs,
		handshakeCh:    make(chan []byte, 1),
		dataCh:         make(chan []byte, 32),
		closed:         make(chan struct{}),
	}
	key := streamKey{registrationID: reg.RegistrationID, streamID: streamID}
	r.mu.Lock()
	r.streams[key] = stream
	r.mu.Unlock()
	defer r.dropStream(key)
	defer stream.shutdown()

	// Send msg1 wrapped in a RelayHandshake frame.
	hsFrame := &relayv1.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body:     &relayv1.RelayMessageFrame_Handshake{Handshake: &relayv1.RelayHandshake{Payload: msg1}},
	}
	hsBytes, err := proto.Marshal(hsFrame)
	if err != nil {
		return fmt.Errorf("noise router: marshal msg1: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, hsBytes); err != nil {
		return fmt.Errorf("noise router: write msg1: %w", err)
	}

	// Await msg2 from the demux loop.
	var msg2 []byte
	select {
	case msg2 = <-stream.handshakeCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-stream.closed:
		return errors.New("noise router: stream closed before handshake completed")
	}
	transport, err := hs.Finish(msg2)
	if err != nil {
		return fmt.Errorf("noise router: finish handshake: %w", err)
	}
	stream.transport = transport
	stream.pendingHS = nil

	// Pump 1: harness → encrypt → RelayData → executor WS.
	pumpErr := make(chan error, 2)
	go func() {
		err := r.pumpHarnessToExecutor(ctx, stream, harness, conn)
		fmt.Fprintf(noiseRouterDebug, "noise router: pump H→E stream=%s err=%v\n", streamID, err)
		pumpErr <- err
	}()
	// Pump 2: dataCh → harness writer.
	go func() {
		err := r.pumpExecutorToHarness(ctx, stream, harness)
		fmt.Fprintf(noiseRouterDebug, "noise router: pump E→H stream=%s err=%v\n", streamID, err)
		pumpErr <- err
	}()

	// First pump exit wins; close the harness side to unblock the other.
	err = <-pumpErr
	fmt.Fprintf(noiseRouterDebug, "noise router: first pump exit stream=%s err=%v\n", streamID, err)
	stream.shutdown()
	_ = harness.Close()
	<-pumpErr // drain the second
	// Best-effort tell the executor we're done with this stream.
	_ = r.sendReset(ctx, conn, streamID, "harness_disconnected")
	return err
}

// noiseRouterDebug is wired by tests via SetNoiseRouterDebug; in
// production it is io.Discard.
var noiseRouterDebug io.Writer = io.Discard

// SetNoiseRouterDebug routes router internal debug lines to w. Test-only.
func SetNoiseRouterDebug(w io.Writer) { noiseRouterDebug = w }

// OpenStreamForBridge is the harness-bridge variant of OpenStream:
// instead of raw bytes, each end of the harness side speaks complete
// JSON-RPC messages (extracted from / wrapped into RelayMessageFrame
// Data frames by the caller).
//
// The router owns the noise framing protocol entirely — outbound
// messages get a 4-byte BE length prefix and chunk across 60 KiB
// noise records, inbound records are reassembled into complete
// messages. This keeps the env-mcp BridgeClient code path unchanged:
// it speaks the same plaintext RelayMessageFrame protocol it always
// has, and the gateway transparently adds/removes the encrypted leg.
//
// Channels:
//   - harnessIn: each item is one complete JSON-RPC message body
//     (without the noise length prefix) the bridge handler extracted
//     from a harness-side RelayData frame. Close to signal harness
//     disconnect.
//   - harnessOut: each item is one complete JSON-RPC message body the
//     router decrypted + reassembled from executor RelayData frames.
//     Caller wraps it back into a RelayMessageFrame{Data{}} for the
//     harness ws. Router closes this when teardown happens.
//
// streamID is the env-mcp-supplied stream_id from the Resume frame.
// It MUST be the same value used by the harness side so codex's noise
// channel prologue is consistent across both peers.
func (r *NoiseRouter) OpenStreamForBridge(
	ctx context.Context,
	envID string,
	streamID string,
	harnessIn <-chan []byte,
	harnessOut chan<- []byte,
) error {
	reg, err := r.store.LookupNoiseExecutorRegistrationByEnv(ctx, envID)
	if err != nil {
		return fmt.Errorf("noise router: no executor for env %q: %w", envID, err)
	}
	conn := r.hub.ConnectionFor(reg.RegistrationID)
	if conn == nil {
		return fmt.Errorf("noise router: executor %q not currently connected", reg.RegistrationID)
	}

	auth := mintHarnessAuthorization(r.hmacKey, reg.RegistrationID, r.identity.PublicKey())
	prologue := noise.Prologue(envID, reg.RegistrationID, streamID)
	hs, msg1, err := noise.StartInitiator(r.identity, reg.PublicKey, prologue, []byte(auth))
	if err != nil {
		return fmt.Errorf("noise router: start initiator: %w", err)
	}

	stream := &activeStream{
		registrationID: reg.RegistrationID,
		streamID:       streamID,
		pendingHS:      hs,
		handshakeCh:    make(chan []byte, 1),
		dataCh:         make(chan []byte, 32),
		closed:         make(chan struct{}),
	}
	key := streamKey{registrationID: reg.RegistrationID, streamID: streamID}
	r.mu.Lock()
	if existing := r.streams[key]; existing != nil {
		r.mu.Unlock()
		return fmt.Errorf("noise router: stream %q already active", streamID)
	}
	r.streams[key] = stream
	r.mu.Unlock()
	defer r.dropStream(key)
	defer stream.shutdown()
	defer close(harnessOut)

	hsFrame := &relayv1.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body:     &relayv1.RelayMessageFrame_Handshake{Handshake: &relayv1.RelayHandshake{Payload: msg1}},
	}
	hsBytes, err := proto.Marshal(hsFrame)
	if err != nil {
		return fmt.Errorf("noise router: marshal msg1: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, hsBytes); err != nil {
		return fmt.Errorf("noise router: write msg1: %w", err)
	}

	var msg2 []byte
	select {
	case msg2 = <-stream.handshakeCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-stream.closed:
		return errors.New("noise router: stream closed before handshake completed")
	}
	transport, err := hs.Finish(msg2)
	if err != nil {
		return fmt.Errorf("noise router: finish handshake: %w", err)
	}
	stream.transport = transport
	stream.pendingHS = nil

	pumpErr := make(chan error, 2)
	go func() {
		err := r.pumpHarnessFramesToExecutor(ctx, stream, harnessIn, conn)
		fmt.Fprintf(noiseRouterDebug, "noise router: bridge pump H→E stream=%s err=%v\n", streamID, err)
		pumpErr <- err
	}()
	go func() {
		err := r.pumpExecutorRecordsToHarness(ctx, stream, harnessOut)
		fmt.Fprintf(noiseRouterDebug, "noise router: bridge pump E→H stream=%s err=%v\n", streamID, err)
		pumpErr <- err
	}()

	err = <-pumpErr
	stream.shutdown()
	<-pumpErr
	_ = r.sendReset(ctx, conn, streamID, "harness_disconnected")
	return err
}

// pumpHarnessFramesToExecutor reads one complete JSON-RPC payload per
// item from harnessIn, frames it with the noise length prefix +
// 60 KiB chunking, encrypts each record, and ships as RelayData.
func (r *NoiseRouter) pumpHarnessFramesToExecutor(
	ctx context.Context,
	stream *activeStream,
	harnessIn <-chan []byte,
	conn *websocket.Conn,
) error {
	for {
		select {
		case <-stream.closed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case payload, ok := <-harnessIn:
			if !ok {
				return nil
			}
			records, err := noise.FrameOutboundMessage(payload)
			if err != nil {
				return fmt.Errorf("frame outbound: %w", err)
			}
			for _, rec := range records {
				ct, err := stream.transport.Encrypt(rec)
				if err != nil {
					return fmt.Errorf("encrypt: %w", err)
				}
				seq := stream.nextSendSeq
				stream.nextSendSeq++
				frame := &relayv1.RelayMessageFrame{
					Version:  1,
					StreamId: stream.streamID,
					Body: &relayv1.RelayMessageFrame_Data{
						Data: &relayv1.RelayData{
							Seq:          seq,
							SegmentIndex: 0,
							SegmentCount: 1,
							Payload:      ct,
						},
					},
				}
				out, mErr := proto.Marshal(frame)
				if mErr != nil {
					return fmt.Errorf("marshal data: %w", mErr)
				}
				if wErr := conn.Write(ctx, websocket.MessageBinary, out); wErr != nil {
					return fmt.Errorf("write data: %w", wErr)
				}
			}
		}
	}
}

// pumpExecutorRecordsToHarness reads decrypted records off dataCh,
// feeds them to a reassembler, and emits complete JSON-RPC messages
// on harnessOut.
func (r *NoiseRouter) pumpExecutorRecordsToHarness(
	ctx context.Context,
	stream *activeStream,
	harnessOut chan<- []byte,
) error {
	var rs noise.InboundReassembler
	for {
		select {
		case <-stream.closed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case record, ok := <-stream.dataCh:
			if !ok {
				return nil
			}
			messages, err := rs.Push(record)
			if err != nil {
				return fmt.Errorf("reassemble: %w", err)
			}
			for _, msg := range messages {
				select {
				case harnessOut <- msg:
				case <-stream.closed:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

func (r *NoiseRouter) pumpHarnessToExecutor(ctx context.Context, stream *activeStream, harness io.Reader, conn *websocket.Conn) error {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-stream.closed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := harness.Read(buf)
		if n > 0 {
			ct, encErr := stream.transport.Encrypt(buf[:n])
			if encErr != nil {
				return fmt.Errorf("encrypt: %w", encErr)
			}
			seq := stream.nextSendSeq
			stream.nextSendSeq++
			frame := &relayv1.RelayMessageFrame{
				Version:  1,
				StreamId: stream.streamID,
				Body: &relayv1.RelayMessageFrame_Data{
					Data: &relayv1.RelayData{
						Seq:          seq,
						SegmentIndex: 0,
						SegmentCount: 1,
						Payload:      ct,
					},
				},
			}
			out, mErr := proto.Marshal(frame)
			if mErr != nil {
				return fmt.Errorf("marshal data: %w", mErr)
			}
			if wErr := conn.Write(ctx, websocket.MessageBinary, out); wErr != nil {
				return fmt.Errorf("write data: %w", wErr)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (r *NoiseRouter) pumpExecutorToHarness(ctx context.Context, stream *activeStream, harness io.Writer) error {
	for {
		select {
		case pt, ok := <-stream.dataCh:
			if !ok {
				return nil
			}
			if _, err := harness.Write(pt); err != nil {
				return fmt.Errorf("write harness: %w", err)
			}
		case <-stream.closed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (r *NoiseRouter) sendReset(ctx context.Context, conn *websocket.Conn, streamID, reason string) error {
	frame := &relayv1.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body:     &relayv1.RelayMessageFrame_Reset_{Reset_: &relayv1.RelayReset{Reason: reason}},
	}
	b, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageBinary, b)
}

func (r *NoiseRouter) resetStream(ctx context.Context, conn *websocket.Conn, stream *activeStream, reason string) {
	key := streamKey{registrationID: stream.registrationID, streamID: stream.streamID}
	r.dropStream(key)
	stream.shutdown()
	_ = r.sendReset(ctx, conn, stream.streamID, reason)
}

func (r *NoiseRouter) dropStream(key streamKey) {
	r.mu.Lock()
	delete(r.streams, key)
	r.mu.Unlock()
}

// ActiveStreamCount returns the number of currently-active streams.
// Test / metrics aid only.
func (r *NoiseRouter) ActiveStreamCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// mintHarnessAuthorization is the canonical HMAC mint shared by the
// NoiseRouter (initiator side) and NoiseHandlers.handleConnect
// (registry side). The two MUST produce identical bytes for a given
// (registration_id, harness_pubkey) — otherwise /validate fails.
func mintHarnessAuthorization(hmacKey []byte, registrationID string, harnessPK noise.PublicKey) string {
	mac := hmac.New(sha256.New, hmacKey)
	for _, part := range []string{
		"noise-relay-harness-auth/v1",
		registrationID,
		harnessPK.Suite,
		harnessPK.X25519PublicKey,
		harnessPK.MLKEM768PublicKey,
	} {
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(part)))
		mac.Write(lenBuf[:])
		mac.Write([]byte(part))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
