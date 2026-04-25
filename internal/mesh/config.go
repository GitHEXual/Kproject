package mesh

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultNamespace        = "kproject-mesh"
	defaultHandshakeTimeout = 8 * time.Second
	defaultDialTimeout      = 10 * time.Second
	defaultPingInterval     = 20 * time.Second
	defaultTopologyInterval = 30 * time.Second
	defaultPeerTTL          = time.Hour
)

type Config struct {
	ListenAddrs    []string
	BootstrapPeers []string
	TargetPeers    []string
	Name           string
	Namespace      string
	ForcePrivate   bool
}

func (c Config) withDefaults() Config {
	if len(c.ListenAddrs) == 0 {
		c.ListenAddrs = []string{
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip4/0.0.0.0/tcp/0",
		}
	}
	if strings.TrimSpace(c.Namespace) == "" {
		c.Namespace = defaultNamespace
	}
	if strings.TrimSpace(c.Name) == "" {
		c.Name = "mesh-node"
	}
	return c
}

func (c Config) Validate() error {
	c = c.withDefaults()

	for _, addr := range c.ListenAddrs {
		if strings.TrimSpace(addr) == "" {
			return fmt.Errorf("listen address cannot be empty")
		}
	}

	seen := make(map[string]struct{})
	for _, addr := range append(append([]string{}, c.BootstrapPeers...), c.TargetPeers...) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return fmt.Errorf("peer address cannot be empty")
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		if _, err := parseAddrInfo(addr); err != nil {
			return err
		}
	}

	return nil
}
