package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config holds process-level settings used for wiring the service.
type Config struct {
	Port   string
	NodeID string
	Peers  []string
}

// Load reads configuration from environment variables with safe defaults.
func Load() Config {
	port := os.Getenv("KVSTORE_PORT")
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		port = "8080"
	}

	portForID := strings.TrimPrefix(port, ":")
	if portForID == "" {
		portForID = "8080"
	}

	nodeID := strings.TrimSpace(os.Getenv("NODE_ID"))
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%s", portForID)
	}

	var peers []string
	rawPeers := strings.TrimSpace(os.Getenv("PEERS"))
	if rawPeers != "" {
		for _, p := range strings.Split(rawPeers, ",") {
			peer := strings.TrimSpace(p)
			if peer == "" {
				continue
			}
			if isSelfPeer(peer, nodeID, portForID) {
				continue
			}
			peers = append(peers, peer)
		}
	}

	return Config{
		Port:   port,
		NodeID: nodeID,
		Peers:  peers,
	}
}

// Addr returns an http listen address, ensuring it includes ":" prefix.
func (c Config) Addr() string {
	if strings.HasPrefix(c.Port, ":") {
		return c.Port
	}
	return ":" + c.Port
}

func isSelfPeer(peer string, nodeID string, port string) bool {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(peer), "/"))
	if normalized == "" {
		return false
	}

	if normalized == strings.ToLower(strings.TrimSpace(nodeID)) {
		return true
	}

	selfHosts := map[string]struct{}{
		fmt.Sprintf("localhost:%s", port): {},
		fmt.Sprintf("127.0.0.1:%s", port): {},
	}

	if normalized == fmt.Sprintf(":%s", port) || normalized == port {
		return true
	}

	if _, ok := selfHosts[normalized]; ok {
		return true
	}

	if strings.HasPrefix(normalized, "http://") || strings.HasPrefix(normalized, "https://") {
		u, err := url.Parse(normalized)
		if err == nil {
			if _, ok := selfHosts[u.Host]; ok {
				return true
			}
		}
	}

	return false
}
