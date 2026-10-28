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
	"strconv"
	"strings"
	"unicode"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

type Interface struct {
	Name string
	Path string
	// MTU is the value of the MTU line of the [Interface] section, 0 without an MTU line or
	// with an empty value.
	MTU uint32
}

const confSuffix = ".conf"

// interfaceName is the awg-quick name rule ([tools] src/wg-quick/linux.bash:43) with a letter,
// digit or '_' first, so a name is never read as an option.
var interfaceName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_=+.-]{0,14}$`)

var showKeywords = map[string]bool{"all": true, "interfaces": true}

var decimalMTU = regexp.MustCompile(`^[1-9][0-9]*$`)

// Discover returns one interface per *.conf file in dir, in file name order, with absolute paths.
// It fails when dir holds none, a name is unsafe, or a file has a [Peer] section or an [Interface]
// MTU that is neither empty nor a decimal number from 1 to 4294967295.
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
		mtu, err := readConfig(path)
		if err != nil {
			return nil, err
		}
		ifaces = append(ifaces, Interface{Name: name, Path: path, MTU: mtu})
	}
	if len(ifaces) == 0 {
		return nil, fmt.Errorf("config directory %s: no *.conf files", dir)
	}
	return ifaces, nil
}

// readConfig returns the MTU of the [Interface] section, 0 without an MTU line or with an empty
// value. It fails on a [Peer] line as the tools read it: comment cut at '#', every space removed,
// compared without case ([tools] src/config.c:459, :621-665). It reads the MTU as awg-quick does
// ([tools] src/wg-quick/linux.bash:50-59): comment cut at '#', key and value split at the first
// '=' and trimmed, the whole line as the value when there is no '=', keys and section headers
// compared without case, and only the last MTU line of the [Interface] section counts.
func readConfig(path string) (uint32, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading config file: %w", err)
	}
	inInterface := false
	mtu, mtuLine := "", 0
	for i, raw := range strings.Split(string(content), "\n") {
		line, _, _ := strings.Cut(raw, "#")
		stripped := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, line)
		if strings.EqualFold(stripped, "[Peer]") {
			return 0, fmt.Errorf("config file %s: [Peer] section on line %d; peers come from the API", filepath.Base(path), i+1)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			value = line
		}
		key = strings.TrimSpace(key)
		if strings.HasPrefix(key, "[") {
			inInterface = strings.EqualFold(key, "[Interface]")
			continue
		}
		if inInterface && strings.EqualFold(key, "MTU") {
			mtu, mtuLine = strings.TrimSpace(value), i+1
		}
	}
	if mtu == "" {
		return 0, nil
	}
	// Decimal only: ip link set mtu reads a leading 0 as octal and 0x as hex, so GetStatus would
	// report a value other than the one the interface gets.
	if decimalMTU.MatchString(mtu) {
		if n, err := strconv.ParseUint(mtu, 10, 32); err == nil {
			return uint32(n), nil
		}
	}
	return 0, fmt.Errorf("config file %s: MTU on line %d is not a decimal number from 1 to 4294967295", filepath.Base(path), mtuLine)
}

// BringUp runs awg-quick up for each interface, then checks each has an address: an interface
// without one would reject every peer.
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
