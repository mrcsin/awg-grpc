// Package node discovers the configured interfaces, brings them up and probes the module
// generation.
package node

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

// Interface is one configured interface: the name awg-quick derives from its file, and the file.
type Interface struct {
	Name string
	Path string
}

const confSuffix = ".conf"

// interfaceName is the awg-quick name rule ([tools] src/wg-quick/linux.bash:43) without a
// leading '-' or '.', so a name is never read as an option.
var interfaceName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_=+.-]{0,14}$`)

// showKeywords are the first arguments awg show reads as keywords, not interface names.
var showKeywords = map[string]bool{"all": true, "interfaces": true}

// Discover returns one interface per *.conf file in dir, in file name order, each with an absolute
// path. It fails when dir holds none, when a name is not a safe interface name, or when a file has
// a [Peer] section.
func Discover(dir string) ([]Interface, error) {
	// awg-quick reads a bare "awg0.conf" as an interface name and loads
	// /etc/amnezia/amneziawg/awg0.conf.conf instead ([tools] src/wg-quick/linux.bash:43).
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving config directory: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading config directory: %w", err)
	}
	var ifaces []Interface
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), confSuffix)
		if !ok || e.IsDir() {
			continue
		}
		if !interfaceName.MatchString(name) || showKeywords[name] {
			return nil, fmt.Errorf("config file %s: interface name %q is not allowed", e.Name(), name)
		}
		path := filepath.Join(dir, e.Name())
		if err := checkNoPeers(path); err != nil {
			return nil, err
		}
		ifaces = append(ifaces, Interface{Name: name, Path: path})
	}
	if len(ifaces) == 0 {
		return nil, fmt.Errorf("config directory %s: no *.conf files", dir)
	}
	return ifaces, nil
}

// checkNoPeers fails when the file has a [Peer] line as the tools read it: comment cut at '#',
// every space removed, compared without case ([tools] src/config.c:459, :621-665).
func checkNoPeers(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}
	for i, raw := range strings.Split(string(content), "\n") {
		line, _, _ := strings.Cut(raw, "#")
		line = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, line)
		if strings.EqualFold(line, "[Peer]") {
			return fmt.Errorf("config file %s: [Peer] section on line %d; peers come from the API", filepath.Base(path), i+1)
		}
	}
	return nil
}

// BringUp runs awg-quick up for each interface in order, then checks that each one is present
// with at least one address. An interface without an address would reject every peer, so it
// stops the start instead of failing each apply.
func BringUp(ctx context.Context, runner awg.Runner, lookup func(name string) ([]netip.Prefix, bool, error), ifaces []Interface) error {
	for _, iface := range ifaces {
		if _, err := runner.Run(ctx, awg.Command{Name: "awg-quick", Args: []string{"up", iface.Path}}); err != nil {
			return fmt.Errorf("bringing up %s: %w", iface.Name, err)
		}
	}
	for _, iface := range ifaces {
		addrs, present, err := lookup(iface.Name)
		if err != nil {
			return fmt.Errorf("looking up %s: %w", iface.Name, err)
		}
		if !present {
			return fmt.Errorf("interface %s is absent after bring-up", iface.Name)
		}
		if len(addrs) == 0 {
			return fmt.Errorf("interface %s has no address; every peer would fall outside it", iface.Name)
		}
	}
	return nil
}
