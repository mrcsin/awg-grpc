package awg

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Field counts of awg show <iface> dump for the tools version the image pins.
const (
	dumpDeviceFields = 29
	dumpPeerFields   = 8
)

// none is how awg show dump prints an unset key, endpoint or allowed IP list.
const none = "(none)"

// ParseDump parses the output of awg show <iface> dump. Fields split on tabs only, because
// I1-I5 values hold spaces. Errors name the line and the field, never the input text.
func ParseDump(out []byte) (Device, error) {
	text := strings.TrimSuffix(string(out), "\n")
	if text == "" {
		return Device{}, errors.New("dump: no device line")
	}
	lines := strings.Split(text, "\n")

	device, err := parseDeviceLine(lines[0])
	if err != nil {
		return Device{}, fmt.Errorf("dump line 1: %w", err)
	}
	for i, line := range lines[1:] {
		peer, err := parsePeerLine(line)
		if err != nil {
			return Device{}, fmt.Errorf("dump line %d: %w", i+2, err)
		}
		device.Peers = append(device.Peers, peer)
	}
	return device, nil
}

func parseDeviceLine(line string) (Device, error) {
	fields, err := splitFields(line, dumpDeviceFields)
	if err != nil {
		return Device{}, err
	}
	var d Device
	if fields[1] != none {
		if d.PublicKey, err = parseKey(fields[1]); err != nil {
			return Device{}, fmt.Errorf("public key: %w", err)
		}
	}
	port, err := strconv.ParseUint(fields[2], 10, 16)
	if err != nil {
		return Device{}, fmt.Errorf("listen port: %w", numError(err))
	}
	d.ListenPort = uint16(port)
	return d, nil
}

func parsePeerLine(line string) (Peer, error) {
	fields, err := splitFields(line, dumpPeerFields)
	if err != nil {
		return Peer{}, err
	}
	var p Peer
	if p.PublicKey, err = parseKey(fields[0]); err != nil {
		return Peer{}, fmt.Errorf("public key: %w", err)
	}
	if fields[1] != none {
		psk, err := parseKey(fields[1])
		if err != nil {
			return Peer{}, fmt.Errorf("preshared key: %w", err)
		}
		p.PresharedKey = NewPresharedKey(psk)
	}
	if fields[2] != none {
		p.Endpoint = fields[2]
	}
	if fields[3] != none {
		for i, cidr := range strings.Split(fields[3], ",") {
			prefix, err := netip.ParsePrefix(cidr)
			if err != nil {
				return Peer{}, fmt.Errorf("allowed IP %d: not a CIDR prefix", i+1)
			}
			p.AllowedIPs = append(p.AllowedIPs, prefix)
		}
	}
	handshake, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return Peer{}, fmt.Errorf("last handshake: %w", numError(err))
	}
	if handshake != 0 {
		p.LastHandshake = time.Unix(handshake, 0).UTC()
	}
	if p.RxBytes, err = strconv.ParseUint(fields[5], 10, 64); err != nil {
		return Peer{}, fmt.Errorf("rx bytes: %w", numError(err))
	}
	if p.TxBytes, err = strconv.ParseUint(fields[6], 10, 64); err != nil {
		return Peer{}, fmt.Errorf("tx bytes: %w", numError(err))
	}
	return p, nil
}

func splitFields(line string, want int) ([]string, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != want {
		return nil, fmt.Errorf("%d fields, want %d", len(fields), want)
	}
	return fields, nil
}

// parseKey decodes a base64 key of 32 bytes. Its errors never quote the input.
func parseKey(b64 string) ([32]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid base64: %w", err)
	}
	if len(raw) != 32 {
		return [32]byte{}, fmt.Errorf("%d bytes, want 32", len(raw))
	}
	return [32]byte(raw), nil
}

// numError drops the quoted input from a strconv.Parse* error, always a *strconv.NumError, and
// keeps its cause.
func numError(err error) error {
	return err.(*strconv.NumError).Err
}
