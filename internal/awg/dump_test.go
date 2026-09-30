package awg

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func prefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

func TestParseDumpFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		want    Device
	}{
		{
			fixture: "no-peers.txt",
			want: Device{
				PublicKey:  mustKey(t, "//biP23cHerOnuCgJIKo7veCiDTTt1R/X2z314+Pogo="),
				ListenPort: 51822,
			},
		},
		{
			fixture: "no-private-key.txt",
			want:    Device{ListenPort: 42982},
		},
		{
			fixture: "psk-mixed.txt",
			want: Device{
				PublicKey:  mustKey(t, "3IZPXStOpj7xcG+jsKS4BPeelm96z1VZafIIhddX01k="),
				ListenPort: 51820,
				Peers: []Peer{
					{
						PublicKey:    mustKey(t, "CY4TOiryEYeopArmdKp/sa61ESKWl1Kbgje4rbFbUHw="),
						PresharedKey: mustPSK(t, fixturePSK),
						AllowedIPs:   prefixes("10.66.0.2/32"),
					},
					{
						PublicKey:    mustKey(t, "ouEjncR2cIQ/iOjX+e8k91AiCA/YXpjZrRLMDJ39Pnc="),
						PresharedKey: mustPSK(t, "nY+zH3jd+Z7xPENEXLED3mF8bthEppxMloca6Cx6RPM="),
						AllowedIPs:   prefixes("10.66.0.3/32"),
					},
					{
						PublicKey:  mustKey(t, "oqNOS0iTnFF8IggAjfLkjRXvFTAsDFVa896865MU4ys="),
						AllowedIPs: prefixes("10.66.0.4/32"),
					},
				},
			},
		},
		{
			fixture: "full.txt",
			want: Device{
				PublicKey:  mustKey(t, "+tJYTAbxDVzZ8a/WIgKkCoNBeXrX451uuqlW7V5NnlY="),
				ListenPort: 51823,
				Peers: []Peer{
					{
						PublicKey:     mustKey(t, "ij+isP0PDb6e0Qlejysg6hgcy+izwRa/mw/RMnbUGGk="),
						PresharedKey:  mustPSK(t, "wY6yc+47yV+7NrIKbYTEnsMRTfnkTXkf8rP1u41TbAw="),
						Endpoint:      "172.20.0.3:51824",
						AllowedIPs:    prefixes("10.67.0.2/32"),
						LastHandshake: time.Unix(1790709091, 0).UTC(),
						RxBytes:       567,
						TxBytes:       874,
					},
					{
						PublicKey:  mustKey(t, "lLks0INSyThw+sM+JDZYt0a4kCk7Tj9EckexdWncqxg="),
						AllowedIPs: prefixes("10.67.0.8/30", "10.67.0.12/30"),
					},
					{
						PublicKey:    mustKey(t, "lj1uEIQdYBX+h1aAi5yfinsbMHv5tmQGqm/iPLthKj4="),
						PresharedKey: mustPSK(t, "pjLJpdDVySwpEs2bu1kTLlXWHI8qmmaEdH/0Ti0S0ME="),
						Endpoint:     "172.20.0.250:51820",
						AllowedIPs:   prefixes("10.67.0.20/32"),
						TxBytes:      1247,
					},
					{
						PublicKey:  mustKey(t, "GSp+2CtxxUfOMjfp2hpw620fKbYgjeixIxXsYpJGHwU="),
						Endpoint:   "[2001:db8::1]:51820",
						AllowedIPs: prefixes("10.67.0.21/32"),
					},
					{
						PublicKey: mustKey(t, "69czXD28b5Q4ZYoy4db3vDKtpmmhZ76OkFeKhqzX7F0="),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got, err := ParseDump(readFixture(t, "dump", tt.fixture))
			if err != nil {
				t.Fatalf("ParseDump: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseDump mismatch\n got: %#v\nwant: %#v", debugDevice(got), debugDevice(tt.want))
			}
		})
	}
}

// debugDevice renders a Device with PSKs as base64, for test failure messages only.
func debugDevice(d Device) string {
	var b strings.Builder
	b.WriteString(d.PublicKey.String())
	for _, p := range d.Peers {
		psk := "(none)"
		if p.PresharedKey != nil {
			psk = Key(p.PresharedKey.Bytes()).String()
		}
		b.WriteString("\n  " + p.PublicKey.String() + " psk=" + psk + " ep=" + p.Endpoint)
		for _, ip := range p.AllowedIPs {
			b.WriteString(" " + ip.String())
		}
		b.WriteString(" hs=" + p.LastHandshake.String())
	}
	return b.String()
}

func TestParseDumpMalformed(t *testing.T) {
	const marker = "LEAKMARK"
	device := strings.TrimSuffix(string(readFixture(t, "dump", "no-peers.txt")), "\n")
	peer := func(fields ...string) string { return device + "\n" + strings.Join(fields, "\t") + "\n" }
	const pub = "oqNOS0iTnFF8IggAjfLkjRXvFTAsDFVa896865MU4ys="

	tests := []struct {
		name     string
		input    string
		wantText []string
	}{
		{
			name:  "empty output",
			input: "",
		},
		{
			name:     "device line with wrong field count",
			input:    "a\t" + marker + "\t51820\n",
			wantText: []string{"line 1", "3 fields", "want 29"},
		},
		{
			name:     "peer line with wrong field count",
			input:    peer(pub, marker),
			wantText: []string{"line 2", "2 fields", "want 8"},
		},
		{
			name:     "peer psk bad base64",
			input:    peer(pub, marker+"!!", "(none)", "(none)", "0", "0", "0", "off"),
			wantText: []string{"line 2", "preshared key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDump([]byte(tt.input))
			if err == nil {
				t.Fatal("ParseDump succeeded, want an error")
			}
			msg := err.Error()
			if strings.Contains(msg, marker) {
				t.Fatalf("error quotes the input: %s", msg)
			}
			for _, want := range tt.wantText {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q lacks %q", msg, want)
				}
			}
		})
	}
}
