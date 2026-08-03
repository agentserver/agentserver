// Package httperrorlog constructs HTTP server error loggers that retain
// actionable failures while dropping noise caused by AgentServer's local TCP
// health probe closing a TLS listener before starting a handshake.
package httperrorlog

import (
	"io"
	"log"
	"net"
	"strings"
)

const tlsHandshakeErrorPrefix = "http: TLS handshake error from "

// New returns a standard HTTP server error logger. It suppresses only a TLS
// handshake EOF whose peer address is loopback; certificate failures, remote
// EOFs, protocol errors, and all non-handshake errors are written unchanged.
func New(destination io.Writer) *log.Logger {
	if destination == nil {
		destination = io.Discard
	}
	return log.New(loopbackProbeEOFFilter{destination: destination}, "", log.LstdFlags)
}

type loopbackProbeEOFFilter struct {
	destination io.Writer
}

func (filter loopbackProbeEOFFilter) Write(contents []byte) (int, error) {
	if isLoopbackTLSHandshakeEOF(string(contents)) {
		return len(contents), nil
	}
	written, err := filter.destination.Write(contents)
	if err == nil && written != len(contents) {
		return written, io.ErrShortWrite
	}
	return written, err
}

func isLoopbackTLSHandshakeEOF(line string) bool {
	line = strings.TrimSuffix(line, "\n")
	if strings.ContainsAny(line, "\r\n") {
		return false
	}
	marker := strings.Index(line, tlsHandshakeErrorPrefix)
	if marker < 0 {
		return false
	}
	remainder := strings.TrimPrefix(line[marker:], tlsHandshakeErrorPrefix)
	address, found := strings.CutSuffix(remainder, ": EOF")
	if !found || address == "" {
		return false
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
