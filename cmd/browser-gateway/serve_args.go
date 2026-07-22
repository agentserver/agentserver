package main

import (
	"flag"
	"io"
	"os"
)

type serveArgs struct {
	ListenAddr string
}

func parseServeArgs(rawArgs []string) (serveArgs, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen-addr", ":8088", "HTTP listen address (env BRG_LISTEN_ADDR)")
	if err := fs.Parse(rawArgs); err != nil {
		return serveArgs{}, err
	}
	if envListen := os.Getenv("BRG_LISTEN_ADDR"); envListen != "" && *listen == ":8088" {
		*listen = envListen
	}
	return serveArgs{ListenAddr: *listen}, nil
}
