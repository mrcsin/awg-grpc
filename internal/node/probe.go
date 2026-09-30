package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

// Probe checks that the host module accepts the newest device attribute the tools know. It
// reads disable-cookies from iface and sets the same value back: the set changes nothing on a
// module of the tools' generation and fails on an older one. It cannot detect a module newer
// than the tools.
func Probe(ctx context.Context, runner awg.Runner, iface, toolsVersion string) error {
	out, err := runner.Run(ctx, awg.Command{Name: "awg", Args: []string{"show", iface, "disable-cookies"}})
	if err != nil {
		return mismatch(toolsVersion, fmt.Errorf("reading disable-cookies of %s: %w", iface, err))
	}
	value := strings.TrimSpace(string(out))
	if value != "on" && value != "off" {
		return mismatch(toolsVersion, fmt.Errorf("disable-cookies of %s is %q, want on or off", iface, value))
	}
	if _, err := runner.Run(ctx, awg.Command{Name: "awg", Args: []string{"set", iface, "disable-cookies", value}}); err != nil {
		return mismatch(toolsVersion, fmt.Errorf("setting disable-cookies of %s: %w", iface, err))
	}
	return nil
}

func mismatch(toolsVersion string, err error) error {
	return fmt.Errorf("generation mismatch between %s%s and the host module: %w", awg.ToolsVersionPrefix, toolsVersion, err)
}
