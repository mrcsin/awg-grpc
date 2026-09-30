package awg

import (
	"fmt"
	"strings"
)

// ToolsVersionPrefix starts the first line of awg --version ([tools] src/wg.c:44-45).
const ToolsVersionPrefix = "amneziawg-tools v"

// ParseToolsVersion returns the bare version from awg --version, e.g. "3.1.20260812".
func ParseToolsVersion(out []byte) (string, error) {
	line, _, _ := strings.Cut(string(out), "\n")
	rest, ok := strings.CutPrefix(line, ToolsVersionPrefix)
	version, _, _ := strings.Cut(rest, " ")
	if !ok || version == "" {
		return "", fmt.Errorf("awg --version: unexpected output %q", line)
	}
	return version, nil
}
