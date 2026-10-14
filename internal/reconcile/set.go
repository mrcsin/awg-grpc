package reconcile

import (
	"strconv"
	"strings"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

const firstExtraFD = 3

// BuildSet returns the one awg set command for changes, and false when they are empty. A preshared
// key travels in an extra file, never in Args.
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
				files = append(files, want.PresharedKey.KeyFile())
			}
		}
	}
	return awg.Command{Name: "awg", Args: args, ExtraFiles: files}, true
}
