// Command awg-grpc owns the AmneziaWG interfaces of one node and serves the management API on a
// unix socket. serve runs the service; healthcheck probes it for the Docker healthcheck.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = "usage: awg-grpc serve|healthcheck"

func main() {
	os.Exit(dispatch(os.Args[1:], os.Getenv, os.Stderr))
}

func dispatch(args []string, getenv func(string) string, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "serve" && args[0] != "healthcheck") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if args[0] == "healthcheck" {
		return healthcheck(context.Background(), socketFile, stderr)
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	cfg, err := configFromEnv(getenv)
	if err != nil {
		logger.Error("reading configuration", "error", err)
		return 1
	}
	return serve(cfg, logger)
}
