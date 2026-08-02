package docker

import (
	"github.com/moby/moby/client"
)

func BuildNetworkOptions(labels map[string]string) client.NetworkCreateOptions {
	enableIPv4 := true
	enableIPv6 := false
	return client.NetworkCreateOptions{
		Driver:     "bridge",
		Scope:      "local",
		EnableIPv4: &enableIPv4,
		EnableIPv6: &enableIPv6,
		Attachable: false,
		Internal:   false,
		Options:    map[string]string{"com.docker.network.bridge.enable_icc": "false"},
		Labels:     labels,
	}
}
