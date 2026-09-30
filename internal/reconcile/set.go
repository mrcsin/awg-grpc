package reconcile

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

// firstExtraFD is the descriptor number the child sees for the first extra file.
const firstExtraFD = 3

// BuildSet returns the one awg set command that applies changes to the interface name, and false
// when the changes are empty. Removals come first, then added and updated peers, each list in the
// order Changes holds it. A peer gets a preshared-key argument when its desired key differs from
// the kernel's; the key travels in an extra file, never in Args. Every argument is rebuilt from
// validated values: keys from 32 bytes, CIDRs from parsed prefixes.
func BuildSet(name string, changes Changes, desired, kernel []awg.Peer) (awg.Command, bool) {
	if len(changes.Added)+len(changes.Removed)+len(changes.Updated) == 0 {
		return awg.Command{}, false
	}
	wantByKey, kernelByKey := byPublicKey(desired), byPublicKey(kernel)

	args := []string{"set", name}
	for _, pub := range changes.Removed {
		args = append(args, "peer", pub.String(), "remove")
	}

	var files [][]byte
	for _, list := range [][]awg.Key{changes.Added, changes.Updated} {
		for _, pub := range list {
			want := wantByKey[pub]
			args = append(args, "peer", pub.String(), "allowed-ips", strings.Join(awg.PrefixStrings(want.AllowedIPs), ","))
			if !samePresharedKey(kernelByKey[pub].PresharedKey, want.PresharedKey) {
				fd := firstExtraFD + len(files)
				args = append(args, "preshared-key", "/dev/fd/"+strconv.Itoa(fd))
				files = append(files, pskFileContent(*want.PresharedKey))
			}
		}
	}
	return awg.Command{Name: "awg", Args: args, ExtraFiles: files}, true
}

// pskFileContent is the key file format parse_keyfile reads: base64 and a newline.
func pskFileContent(k awg.PresharedKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(k.Bytes()) + "\n")
}
