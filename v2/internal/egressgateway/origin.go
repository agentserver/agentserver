package egressgateway

import (
	"net"
	"strings"
)

// isLoopbackHost is used only to permit cleartext Core connections in the
// explicitly insecure local-development mode. Production callers require
// HTTPS and mTLS before they reach any credential endpoint.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
