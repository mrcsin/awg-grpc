package awg

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixturePSK is the PSK of the first peer in testdata/dump/psk-mixed.txt, a throwaway test key.
const fixturePSK = "AXzkhaUcx4Y8J39+Z6udReAPJCDZjjvZcyoPjPcIHFE="

func mustKey(t *testing.T, b64 string) [32]byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		t.Fatalf("bad test key %q: len %d, err %v", b64, len(raw), err)
	}
	return [32]byte(raw)
}

func mustPSK(t *testing.T, b64 string) *PresharedKey {
	t.Helper()
	return NewPresharedKey(mustKey(t, b64))
}

// leakForms returns every rendering of a key that must never appear in output.
func leakForms(raw [32]byte) []string {
	return []string{
		base64.StdEncoding.EncodeToString(raw[:]),
		hex.EncodeToString(raw[:]),
		strings.ToUpper(hex.EncodeToString(raw[:])),
		fmt.Sprint(raw),
		fmt.Sprint(raw[:]),
		string(raw[:]),
	}
}

func TestPresharedKeyNeverPrints(t *testing.T) {
	psk := mustPSK(t, fixturePSK)
	peer := Peer{
		PublicKey:     mustKey(t, "CY4TOiryEYeopArmdKp/sa61ESKWl1Kbgje4rbFbUHw="),
		PresharedKey:  psk,
		Endpoint:      "172.20.0.3:51824",
		AllowedIPs:    []netip.Prefix{netip.MustParsePrefix("10.66.0.2/32")},
		LastHandshake: time.Unix(1790709091, 0).UTC(),
		RxBytes:       567,
		TxBytes:       874,
	}
	device := Device{
		PublicKey:  mustKey(t, "3IZPXStOpj7xcG+jsKS4BPeelm96z1VZafIIhddX01k="),
		ListenPort: 51820,
		Peers:      []Peer{peer},
	}

	subjects := []struct {
		name  string
		value any
	}{
		{"key", *psk},
		{"key pointer", psk},
		{"peer", peer},
		{"peer pointer", &peer},
		{"device", device},
		{"device pointer", &device},
	}
	verbs := []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%q", "%d"}

	for _, s := range subjects {
		for _, verb := range verbs {
			t.Run(s.name+" "+verb, func(t *testing.T) {
				assertRedacted(t, fmt.Sprintf(verb, s.value))
			})
		}
		t.Run(s.name+" slog text", func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("m", "subject", s.value)
			assertRedacted(t, buf.String())
		})
		t.Run(s.name+" slog json", func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("m", "subject", s.value)
			assertRedacted(t, buf.String())
		})
	}
}

func assertRedacted(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "***") {
		t.Errorf("output lacks ***: %s", out)
	}
	raw := [32]byte(mustKey(t, fixturePSK))
	for _, form := range leakForms(raw) {
		if strings.Contains(out, form) {
			t.Fatalf("output holds the key as %q: %s", form, out)
		}
	}
}

func TestPresharedKeyKeyFile(t *testing.T) {
	psk := mustPSK(t, fixturePSK)
	if got, want := string(psk.KeyFile()), fixturePSK+"\n"; got != want {
		t.Fatalf("KeyFile() = %q, want %q", got, want)
	}
}

func TestKeyString(t *testing.T) {
	const text = "CY4TOiryEYeopArmdKp/sa61ESKWl1Kbgje4rbFbUHw="
	k := Key(mustKey(t, text))
	for _, got := range []string{k.String(), fmt.Sprint(k), fmt.Sprintf("%s", k)} {
		if got != text {
			t.Errorf("key prints as %q, want %q", got, text)
		}
	}
}

func TestPrefixStrings(t *testing.T) {
	tests := []struct {
		name     string
		prefixes []netip.Prefix
		want     []string
	}{
		{name: "none", want: []string{}},
		{name: "in order", prefixes: prefixes("10.8.1.2/32", "10.8.1.0/28"), want: []string{"10.8.1.2/32", "10.8.1.0/28"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrefixStrings(tt.prefixes); !slices.Equal(got, tt.want) || got == nil {
				t.Errorf("PrefixStrings() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
