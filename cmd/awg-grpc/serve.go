package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/node"
	"github.com/mrcsin/awg-grpc/internal/server"
)

const moduleVersionFile = "/sys/module/amneziawg/version"

const socketMode = 0o660

func serve(cfg config, logger *slog.Logger) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, cfg, awg.ExecRunner{}, server.NetLookup, logger); err != nil {
		logger.Error("serve failed", "error", err)
		return 1
	}
	return 0
}

// run returns every startup failure before the socket exists. Interfaces stay up after it
// returns, because they live in the container network namespace.
func run(ctx context.Context, cfg config, runner awg.Runner, lookup server.Lookup, logger *slog.Logger) error {
	names, toolsVersion, err := node.Start(ctx, runner, lookup, cfg.ConfigDir)
	if err != nil {
		return err
	}

	lis, err := listen(cfg)
	if err != nil {
		return err
	}
	defer removeSocket(cfg.Socket, logger)

	srv := server.NewGRPCServer(runner, lookup, names, server.Versions{
		Wrapper:           version,
		Tools:             toolsVersion,
		ModuleVersionFile: moduleVersionFile,
	}, logger)

	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
	logger.Info("serving", "socket", cfg.Socket, "interfaces", names, "wrapper_version", version, "tools_version", toolsVersion)

	select {
	case <-ctx.Done():
		srv.GracefulStop()
		<-served
		logger.Info("stopped")
		return nil
	case err := <-served:
		return fmt.Errorf("serving on %s: %w", cfg.Socket, err)
	}
}

func listen(cfg config) (net.Listener, error) {
	if err := os.Remove(cfg.Socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}
	lis, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return nil, fmt.Errorf("listening: %w", err)
	}
	if err := os.Chmod(cfg.Socket, socketMode); err != nil {
		_ = lis.Close()
		return nil, fmt.Errorf("setting socket mode: %w", err)
	}
	if cfg.SocketGID != noSocketGID {
		if err := os.Chown(cfg.Socket, -1, cfg.SocketGID); err != nil {
			_ = lis.Close()
			return nil, fmt.Errorf("setting socket group: %w", err)
		}
	}
	return lis, nil
}

// Closing the listener usually has already removed the socket file.
func removeSocket(path string, logger *slog.Logger) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.Warn("removing socket", "error", err)
	}
}
