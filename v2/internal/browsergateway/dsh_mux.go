package browsergateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

func readBoundedBody(request *http.Request, maximum int64) ([]byte, error) {
	if request.Body == nil {
		return nil, errors.New("request body is required")
	}
	defer request.Body.Close()
	reader := io.LimitReader(request.Body, maximum+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, errors.New("request body exceeds size limit")
	}
	return data, nil
}

type dshMuxConnection struct {
	gateway   *DSHGateway
	socket    *websocket.Conn
	bearer    string
	writeMu   sync.Mutex
	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc
}

func (gateway *DSHGateway) serveMux(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.NotFound(response, request)
		return
	}
	if !gateway.originAllowed(request) {
		http.Error(response, "origin not allowed", http.StatusForbidden)
		return
	}
	if _, err := extractDSHBearer(request.Header); err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	// originAllowed performed the exact scheme/authority comparison above.
	// OriginPatterns supplies the library's host-level CSWSH check for the
	// configured split-origin frontend as well.
	patterns := make([]string, 0, len(gateway.config.AllowedOrigins))
	for _, allowed := range gateway.config.AllowedOrigins {
		if parsed, parseErr := url.Parse(allowed); parseErr == nil && parsed.Hostname() != "" {
			patterns = append(patterns, parsed.Host)
		}
	}
	socket, err := websocket.Accept(response, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled, OriginPatterns: patterns})
	if err != nil {
		return
	}
	socket.SetReadLimit(2 * 1024 * 1024)
	bearer, _ := extractDSHBearer(request.Header)
	connection := &dshMuxConnection{gateway: gateway, socket: socket, bearer: bearer, streams: make(map[string]context.CancelFunc)}
	connection.run(request.Context())
}

func (connection *dshMuxConnection) run(ctx context.Context) {
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(heartbeatCtx, 5*time.Second)
				if err := connection.socket.Ping(pingCtx); err != nil {
					cancel()
					return
				}
				cancel()
			}
		}
	}()
	defer func() {
		connection.streamsMu.Lock()
		for _, cancel := range connection.streams {
			cancel()
		}
		connection.streams = nil
		connection.streamsMu.Unlock()
		_ = connection.socket.Close(websocket.StatusNormalClosure, "")
	}()
	for {
		typeID, data, err := connection.socket.Read(ctx)
		if err != nil {
			return
		}
		if typeID != websocket.MessageText {
			_ = connection.socket.Close(websocket.StatusUnsupportedData, "text messages required")
			return
		}
		message, err := parseDSHStreamClientMessage(data)
		if err != nil {
			_ = connection.socket.Close(websocket.StatusPolicyViolation, "invalid stream message")
			return
		}
		switch message.Type {
		case "open":
			connection.open(ctx, message)
		case "item": // DSH streams used by the shipped client have no uplink.
		case "end":
		case "cancel":
			connection.cancel(message.StreamID)
		}
	}
}

func (connection *dshMuxConnection) open(parent context.Context, message dshStreamClientMessage) {
	connection.streamsMu.Lock()
	if _, exists := connection.streams[message.StreamID]; exists {
		connection.streamsMu.Unlock()
		_ = connection.socket.Close(websocket.StatusPolicyViolation, "duplicate stream id")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	connection.streams[message.StreamID] = cancel
	connection.streamsMu.Unlock()
	go func() {
		defer func() {
			connection.streamsMu.Lock()
			delete(connection.streams, message.StreamID)
			connection.streamsMu.Unlock()
		}()
		connection.serveStream(ctx, message)
	}()
}

func (connection *dshMuxConnection) serveStream(ctx context.Context, message dshStreamClientMessage) {
	var payload dshPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		connection.write(dshStreamError(message.StreamID, "gateway/bad-request", "invalid stream payload", nil))
		return
	}
	args := map[string]json.RawMessage{}
	if payload.Args != nil {
		_ = json.Unmarshal(payload.Args, &args)
	}
	if message.Endpoint == "$events" {
		connection.write(dshStreamItem(message.StreamID, map[string]any{"type": "ready", "clientId": "agentserver", "host": map[string]any{"home": connection.gateway.config.Home}}))
		events, stop := connection.gateway.subscribeRemoteEvents(ctx)
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				frame := event.Frame
				if frame == nil {
					frame = map[string]any{"type": "emit", "event": event.Name, "args": event.Args}
				}
				if !connection.write(dshStreamItem(message.StreamID, frame)) {
					return
				}
			}
		}
	}
	stream, err := connection.gateway.openStream(ctx, connection.bearer, args, message.Endpoint)
	if err != nil {
		code, msg, details := connection.gateway.mapError(err)
		connection.write(dshStreamError(message.StreamID, code, msg, details))
		return
	}
	for value := range stream {
		if !connection.write(dshStreamItem(message.StreamID, value)) {
			return
		}
	}
	connection.write(dshStreamEnd(message.StreamID))
}

func (connection *dshMuxConnection) cancel(id string) {
	connection.streamsMu.Lock()
	cancel := connection.streams[id]
	connection.streamsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (connection *dshMuxConnection) write(value any) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	return connection.socket.Write(context.Background(), websocket.MessageText, raw) == nil
}

func (gateway *DSHGateway) openStream(ctx context.Context, bearer string, args map[string]json.RawMessage, endpoint string) (<-chan any, error) {
	out := make(chan any, 128)
	switch endpoint {
	case "session/follow":
		var request struct {
			Address         map[string]any `json:"address"`
			AssistantStream bool           `json:"assistantStream"`
			MaxMessages     int            `json:"maxMessages"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, err
		}
		id := stringField(request.Address, "sessionId")
		state, err := gateway.state(ctx, bearer, id)
		if err != nil {
			return nil, err
		}
		go func() {
			defer close(out)
			updates, stop := state.subscribe(ctx)
			defer stop()
			events, header, cursor := state.snapshot()
			records := make([]any, 0, len(events))
			for _, event := range events {
				records = append(records, eventValue(event))
			}
			hasMore := false
			if request.MaxMessages > 0 && len(records) > request.MaxMessages {
				records = records[len(records)-request.MaxMessages:]
				hasMore = true
			}
			snapshot := map[string]any{"type": "snapshot", "header": header, "cursor": cursor, "records": records, "hasMore": hasMore, "projections": map[string]any{"asOfSeq": cursor, "values": dshProjectionValues(state.permissionMode())}}
			if request.AssistantStream {
				snapshot["assistantStream"] = state.assistantBaseline()
			}
			out <- snapshot
			assistantRevision := int64(0)
			if request.AssistantStream {
				if baseline, ok := snapshot["assistantStream"].(map[string]any); ok {
					assistantRevision = numberField(baseline, "revision")
				}
			}
			for {
				select {
				case <-ctx.Done():
					return
				case update, ok := <-updates:
					if !ok {
						return
					}
					if update.event != nil {
						event := *update.event
						if event.Seq > cursor {
							out <- eventValue(event)
						}
					}
					if request.AssistantStream && update.assistantFrame != nil {
						revision := numberField(update.assistantFrame, "revision")
						if revision > assistantRevision {
							assistantRevision = revision
							out <- map[string]any{"type": "assistant-stream", "frame": update.assistantFrame}
						}
					}
				}
			}
		}()
	case "session/control":
		go func() {
			defer close(out)
			out <- map[string]any{"type": "baseline", "value": map[string]any{"projections": gateway.projectionBaseline()}}
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			last := map[string]string{}
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					gateway.mu.Lock()
					states := make([]*dshSessionState, 0, len(gateway.sessions))
					for _, state := range gateway.sessions {
						states = append(states, state)
					}
					gateway.mu.Unlock()
					for _, state := range states {
						sessionID, mode, seq := state.projection()
						if last[sessionID] == mode {
							continue
						}
						last[sessionID] = mode
						if seq < 0 {
							continue
						}
						out <- map[string]any{"type": "projection", "sessionId": sessionID, "key": "permissions", "value": map[string]any{"currentValue": mode}, "seq": seq}
					}
				}
			}
		}()
	case "workspace/follow":
		go func() {
			defer close(out)
			if sessions, err := gateway.backend.ListSessions(ctx, bearer, gateway.config.WorkspaceID); err == nil {
				for _, session := range sessions.Sessions {
					gateway.installSession(session)
				}
			}
			out <- map[string]any{"type": "baseline", "value": map[string]any{"items": []any{gateway.workspaceView()}, "archivedSessionIds": []string{}, "pinnedSessionIds": []string{}}}
			updates, stop := gateway.subscribeWorkspace(ctx)
			defer stop()
			for {
				select {
				case <-ctx.Done():
					return
				case frame, ok := <-updates:
					if !ok {
						return
					}
					out <- frame
				}
			}
		}()
	default:
		return nil, errors.New("DSH stream endpoint is not implemented")
	}
	return out, nil
}

func (gateway *DSHGateway) projectionBaseline() map[string]any {
	result := map[string]any{}
	gateway.mu.Lock()
	states := make([]*dshSessionState, 0, len(gateway.sessions))
	for _, state := range gateway.sessions {
		states = append(states, state)
	}
	gateway.mu.Unlock()
	for _, state := range states {
		state.mu.Lock()
		asOfSeq := state.nextSeq - 1
		if len(state.events) == 0 {
			asOfSeq = -1
		}
		result[state.session.SessionID] = map[string]any{"asOfSeq": asOfSeq, "values": dshProjectionValues(state.session.PermissionMode)}
		state.mu.Unlock()
	}
	return result
}
