package codexwire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

var ErrFrameTooLarge = errors.New("Codex wire frame exceeds configured byte limit")

// Decoder reads newline-delimited Codex wire messages with an explicit bound.
type Decoder struct {
	reader        *bufio.Reader
	maxFrameBytes int
	maxJSONNodes  int
}

func NewDecoder(reader io.Reader, maxFrameBytes int) (*Decoder, error) {
	if reader == nil {
		return nil, errors.New("reader is required")
	}
	if err := validateMaxFrameBytes(maxFrameBytes); err != nil {
		return nil, err
	}
	bufferBytes := min(maxFrameBytes+2, 64*1024)
	return &Decoder{
		reader:        bufio.NewReaderSize(reader, bufferBytes),
		maxFrameBytes: maxFrameBytes,
		maxJSONNodes:  DefaultMaxJSONNodes,
	}, nil
}

// Next returns the next non-blank frame. A final frame without a newline is
// accepted, matching stock Codex's stdio behavior.
func (d *Decoder) Next() (Message, error) {
	for {
		line, err := d.readLine()
		if err != nil {
			return Message{}, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		message, err := parseWithNodeLimit(line, d.maxJSONNodes)
		if err != nil {
			return Message{}, fmt.Errorf("decode Codex wire frame: %w", err)
		}
		return message, nil
	}
}

func (d *Decoder) readLine() ([]byte, error) {
	line := make([]byte, 0, min(d.maxFrameBytes, 64*1024))
	for {
		fragment, err := d.reader.ReadSlice('\n')
		if len(line)+len(fragment) > d.maxFrameBytes+2 {
			return nil, ErrFrameTooLarge
		}
		line = append(line, fragment...)

		switch {
		case err == nil:
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if len(line) > d.maxFrameBytes {
				return nil, ErrFrameTooLarge
			}
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			if len(line) > d.maxFrameBytes {
				return nil, ErrFrameTooLarge
			}
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, io.EOF
			}
			if len(line) > d.maxFrameBytes {
				return nil, ErrFrameTooLarge
			}
			return line, nil
		default:
			return nil, fmt.Errorf("read Codex wire frame: %w", err)
		}
	}
}

// Encoder writes complete newline-delimited Codex wire messages. It is safe
// for concurrent callers and validates the envelope before writing bytes.
type Encoder struct {
	writer        io.Writer
	maxFrameBytes int
	mu            sync.Mutex
}

func NewEncoder(writer io.Writer, maxFrameBytes int) (*Encoder, error) {
	if writer == nil {
		return nil, errors.New("writer is required")
	}
	if err := validateMaxFrameBytes(maxFrameBytes); err != nil {
		return nil, err
	}
	return &Encoder{writer: writer, maxFrameBytes: maxFrameBytes}, nil
}

func (e *Encoder) Write(value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Codex wire frame: %w", err)
	}
	if len(frame) > e.maxFrameBytes {
		return ErrFrameTooLarge
	}
	if _, err := Parse(frame); err != nil {
		return fmt.Errorf("validate outbound Codex wire frame: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := writeAll(e.writer, frame); err != nil {
		return fmt.Errorf("write Codex wire frame: %w", err)
	}
	if err := writeAll(e.writer, []byte{'\n'}); err != nil {
		return fmt.Errorf("terminate Codex wire frame: %w", err)
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

type readResult struct {
	message Message
	err     error
}

// Peer combines one encoder with a single bounded background decoder. Receive
// has context cancellation without starting an unbounded goroutine per call.
type Peer struct {
	encoder  *Encoder
	incoming <-chan readResult
}

func NewPeer(reader io.Reader, writer io.Writer, maxFrameBytes, incomingBuffer int) (*Peer, error) {
	if incomingBuffer <= 0 {
		return nil, errors.New("incoming buffer must be positive")
	}
	decoder, err := NewDecoder(reader, maxFrameBytes)
	if err != nil {
		return nil, err
	}
	encoder, err := NewEncoder(writer, maxFrameBytes)
	if err != nil {
		return nil, err
	}

	incoming := make(chan readResult, incomingBuffer)
	go func() {
		defer close(incoming)
		for {
			message, err := decoder.Next()
			incoming <- readResult{message: message, err: err}
			if err != nil {
				return
			}
		}
	}()

	return &Peer{encoder: encoder, incoming: incoming}, nil
}

func (p *Peer) Send(value any) error {
	return p.encoder.Write(value)
}

func (p *Peer) Receive(ctx context.Context) (Message, error) {
	if ctx == nil {
		return Message{}, errors.New("context is required")
	}
	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case result, ok := <-p.incoming:
		if !ok {
			return Message{}, io.EOF
		}
		return result.message, result.err
	}
}

func validateMaxFrameBytes(maxFrameBytes int) error {
	if maxFrameBytes <= 0 {
		return errors.New("max frame bytes must be positive")
	}
	maxInt := int(^uint(0) >> 1)
	if maxFrameBytes > maxInt-2 {
		return errors.New("max frame bytes is too large")
	}
	return nil
}
