package main

import (
	"fmt"
	"strconv"
)

const (
	envSocket    = "AWG_GRPC_SOCKET"
	envConfigDir = "AWG_GRPC_CONFIG_DIR"
	envSocketGID = "AWG_GRPC_SOCKET_GID"

	defaultSocket    = "/run/awg-grpc/awg.sock"
	defaultConfigDir = "/etc/amnezia/amneziawg"

	// noSocketGID leaves the socket in the group of the process.
	noSocketGID = -1
)

type config struct {
	Socket    string
	ConfigDir string
	SocketGID int
}

func configFromEnv(getenv func(string) string) (config, error) {
	cfg := config{Socket: defaultSocket, ConfigDir: defaultConfigDir, SocketGID: noSocketGID}
	if v := getenv(envSocket); v != "" {
		cfg.Socket = v
	}
	if v := getenv(envConfigDir); v != "" {
		cfg.ConfigDir = v
	}
	if v := getenv(envSocketGID); v != "" {
		gid, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return config{}, fmt.Errorf("%s=%q: want a numeric group ID", envSocketGID, v)
		}
		cfg.SocketGID = int(gid)
	}
	return cfg, nil
}
