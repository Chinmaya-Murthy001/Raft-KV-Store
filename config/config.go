package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds process-level settings used for wiring the service.
type Config struct {
	Port              string
	RaftPort          string
	NodeID            string
	DataDir           string
	SnapshotThreshold int
	Peers             []string
	NodeKV            map[string]string
	NodeRaft          map[string]string
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

	raftPort := strings.TrimSpace(os.Getenv("RAFT_PORT"))
	if raftPort == "" {
		if base, err := strconv.Atoi(portForID); err == nil {
			raftPort = fmt.Sprintf("%d", base+1000)
		} else {
			raftPort = "9080"
		}
	}

	raftPortForID := strings.TrimPrefix(raftPort, ":")
	if raftPortForID == "" {
		raftPortForID = "9080"
	}

	snapshotThreshold := 50
	if raw := strings.TrimSpace(os.Getenv("RAFT_SNAPSHOT_THRESHOLD")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			snapshotThreshold = parsed
		}
	}

	var peers []string
	var peerIDs []string
	rawPeers := strings.TrimSpace(os.Getenv("PEERS"))
	if rawPeers == "" {
		// Compatibility alias used by some harnesses/docs.
		rawPeers = strings.TrimSpace(os.Getenv("RAFT_NODES"))
	}
	rawPeerIDs := strings.TrimSpace(os.Getenv("PEER_IDS"))
	if rawPeerIDs != "" {
		for _, id := range strings.Split(rawPeerIDs, ",") {
			trimmed := strings.TrimSpace(id)
			if trimmed != "" {
				peerIDs = append(peerIDs, trimmed)
			}
		}
	}
	if rawPeers != "" {
		for _, p := range strings.Split(rawPeers, ",") {
			peer := strings.TrimSpace(p)
			if peer == "" {
				continue
			}
			if isSelfPeer(peer, nodeID, raftPortForID) {
				continue
			}
			peers = append(peers, peer)
		}
	}

	nodeKV := map[string]string{
		nodeID: fmt.Sprintf("http://localhost%s", (&Config{Port: port}).Addr()),
	}
	nodeRaft := map[string]string{
		nodeID: fmt.Sprintf("http://localhost%s", (&Config{RaftPort: raftPort}).RaftAddr()),
	}
	for i, peer := range peers {
		if i >= len(peerIDs) {
			break
		}
		id := peerIDs[i]
		nodeRaft[id] = normalizeURL(peer)
		if kvURL, ok := deriveKVURLFromRaftPeer(peer); ok {
			nodeKV[id] = kvURL
		}
	}

	return Config{
		Port:              port,
		RaftPort:          raftPort,
		NodeID:            nodeID,
		DataDir:           dataDir(nodeID),
		SnapshotThreshold: snapshotThreshold,
		Peers:             peers,
		NodeKV:            nodeKV,
		NodeRaft:          nodeRaft,
	}
}

// Addr returns an http listen address, ensuring it includes ":" prefix.
func (c Config) Addr() string {
	if strings.HasPrefix(c.Port, ":") {
		return c.Port
	}
	return ":" + c.Port
}

// KVURL returns client-facing base URL for this node.
func (c Config) KVURL() string {
	return "http://localhost" + c.Addr()
}

// RaftAddr returns a listen address for internal raft RPC traffic.
func (c Config) RaftAddr() string {
	if strings.HasPrefix(c.RaftPort, ":") {
		return c.RaftPort
	}
	return ":" + c.RaftPort
}

// RaftURL returns internal raft RPC base URL for this node.
func (c Config) RaftURL() string {
	return "http://localhost" + c.RaftAddr()
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

func normalizeURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return trimmed
	}
	if strings.HasSuffix(trimmed, "/") {
		return strings.TrimSuffix(trimmed, "/")
	}
	return trimmed
}

func deriveKVURLFromRaftPeer(peer string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(peer))
	if err != nil || u.Host == "" {
		return "", false
	}

	host := u.Hostname()
	portText := u.Port()
	if host == "" || portText == "" {
		return "", false
	}

	raftPort, err := strconv.Atoi(portText)
	if err != nil {
		return "", false
	}

	kvPort := raftPort - 1000
	if kvPort <= 0 {
		return "", false
	}

	return fmt.Sprintf("%s://%s:%d", u.Scheme, host, kvPort), true
}

func dataDir(nodeID string) string {
	if v := strings.TrimSpace(os.Getenv("RAFT_DATA_DIR")); v != "" {
		return filepath.Join(v, nodeID)
	}
	return filepath.Join(".", "data", nodeID)
}
