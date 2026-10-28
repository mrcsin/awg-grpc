package node

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

// Start runs the startup steps before the socket (Discover, BringUp, awg --version, Probe on the
// first interface). It returns the interfaces in file name order and the tools version.
func Start(ctx context.Context, runner awg.Runner, lookup func(name string) ([]netip.Prefix, bool, error), dir string) (ifaces []Interface, toolsVersion string, err error) {
	ifaces, err = Discover(dir)
	if err != nil {
		return nil, "", err
	}
	if err := BringUp(ctx, runner, lookup, ifaces); err != nil {
		return nil, "", err
	}
	toolsVersion, err = readToolsVersion(ctx, runner)
	if err != nil {
		return nil, "", err
	}
	if err := Probe(ctx, runner, ifaces[0].Name, toolsVersion); err != nil {
		return nil, "", err
	}
	return ifaces, toolsVersion, nil
}

func readToolsVersion(ctx context.Context, runner awg.Runner) (string, error) {
	out, err := runner.Run(ctx, awg.Command{Name: "awg", Args: []string{"--version"}})
	if err != nil {
		return "", fmt.Errorf("reading tools version: %w", err)
	}
	v, err := awg.ParseToolsVersion(out)
	if err != nil {
		return "", fmt.Errorf("parsing tools version: %w", err)
	}
	return v, nil
}
