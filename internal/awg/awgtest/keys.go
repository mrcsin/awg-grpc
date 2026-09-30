package awgtest

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// KeyForms returns every text form of a key that a leak check looks for: the raw bytes, standard
// and unpadded base64, and hex in both cases.
func KeyForms(key []byte) []string {
	return []string{
		string(key),
		base64.StdEncoding.EncodeToString(key),
		base64.RawStdEncoding.EncodeToString(key),
		hex.EncodeToString(key),
		strings.ToUpper(hex.EncodeToString(key)),
	}
}
