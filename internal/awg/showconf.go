package awg

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Param is one interface parameter from awg showconf, in the form a client config carries it.
type Param struct {
	Key   string
	Value string
}

// paramKey admits only the tools' own key names, because usher writes each pair into client
// configs.
var paramKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// serverOnlyKeys never reach a client config. The tools match keys case-insensitively
// ([tools] src/config.c:40), so the filter does too.
var serverOnlyKeys = []string{"PrivateKey", "ListenPort", "FwMark"}

// ParseClientParams reads the [Interface] section of awg showconf <iface> and returns its
// parameters in order, without PrivateKey, ListenPort and FwMark and without values 0 or off.
// It stops at the first [Peer]. Errors name the line, never its text.
func ParseClientParams(out []byte) ([]Param, error) {
	params := []Param{}
	inInterface := false
	for i, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !inInterface {
			if line != "[Interface]" {
				return nil, fmt.Errorf("showconf line %d: want [Interface]", i+1)
			}
			inInterface = true
			continue
		}
		if line == "[Peer]" {
			break
		}
		if strings.HasPrefix(line, "[") {
			return nil, fmt.Errorf("showconf line %d: unexpected section", i+1)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("showconf line %d: no key = value pair", i+1)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !paramKey.MatchString(key) {
			return nil, fmt.Errorf("showconf line %d: key does not match %s", i+1, paramKey)
		}
		if isServerOnly(key) || value == "0" || value == "off" {
			continue
		}
		params = append(params, Param{Key: key, Value: value})
	}
	if !inInterface {
		return nil, errors.New("showconf: no [Interface] section")
	}
	return params, nil
}

func isServerOnly(key string) bool {
	return slices.ContainsFunc(serverOnlyKeys, func(k string) bool { return strings.EqualFold(k, key) })
}
