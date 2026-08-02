package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const probeTimeout = 2 * time.Second

type probeConnection interface {
	Close() error
}

type probeDialer func(context.Context, string, string) (probeConnection, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr, dialProbe))
}

func run(arguments []string, stderr io.Writer, dial probeDialer) int {
	if len(arguments) != 2 || arguments[0] != "tcp" || !strings.HasPrefix(arguments[1], "--address=") {
		writeUsage(stderr)
		return 2
	}
	address := strings.TrimPrefix(arguments[1], "--address=")
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" || host == "" {
		fmt.Fprintln(stderr, "agentserver-probe: address must be an explicit loopback TCP host:port")
		return 2
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || ip.String() != host {
		fmt.Fprintln(stderr, "agentserver-probe: address must use a canonical loopback IP")
		return 2
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 || strconv.FormatUint(portNumber, 10) != port {
		fmt.Fprintln(stderr, "agentserver-probe: port must be a canonical integer between 1 and 65535")
		return 2
	}
	if dial == nil {
		fmt.Fprintln(stderr, "agentserver-probe: dialer is unavailable")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	connection, err := dial(ctx, "tcp", address)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-probe: %v\n", err)
		return 1
	}
	if err := connection.Close(); err != nil {
		fmt.Fprintf(stderr, "agentserver-probe: close: %v\n", err)
		return 1
	}
	return 0
}

func dialProbe(ctx context.Context, network, address string) (probeConnection, error) {
	if ctx == nil {
		return nil, errors.New("probe context is required")
	}
	return (&net.Dialer{Timeout: probeTimeout, KeepAlive: -1}).DialContext(ctx, network, address)
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: agentserver-probe tcp --address=127.0.0.1:PORT")
}
