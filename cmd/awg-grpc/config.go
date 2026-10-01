package main

import (
	"fmt"
	"strconv"
)

const (
	envSocketGID = "AWG_GRPC_SOCKET_GID"

	socketFile = "/run/awg-grpc/awg.sock"
	configDir  = "/etc/amnezia/amneziawg"

	// noSocketGID leaves the socket in the group of the process.
	noSocketGID = -1
)

type config struct {
	Socket    string
	ConfigDir string
	SocketGID int
}

func configFromEnv(getenv func(string) string) (config, error) {
	cfg := config{Socket: socketFile, ConfigDir: configDir, SocketGID: noSocketGID}
	if v := getenv(envSocketGID); v != "" {
		gid, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return config{}, fmt.Errorf("%s=%q: want a numeric group ID", envSocketGID, v)
		}
		cfg.SocketGID = int(gid)
	}
	return cfg, nil
}
