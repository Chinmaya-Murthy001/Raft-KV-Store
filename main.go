package main

import (
	"context"
	"net/http"
	"os"

	"kvstore/api"
	"kvstore/config"
	"kvstore/raft"
	"kvstore/store"
	"kvstore/utils"
)

func main() {
	cfg := config.Load()
	logger := utils.NewLogger()
	s := store.NewDefaultStore()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		logger.Fatalf("mkdir data dir: %v", err)
	}

	raftNode := raft.NewNode(cfg.NodeID, cfg.RaftPort, cfg.Peers, logger.Logger)
	raftNode.BindStore(s)
	raftNode.SetSnapshotThreshold(cfg.SnapshotThreshold)
	if err := raftNode.InitPersistence(cfg.DataDir); err != nil {
		logger.Fatalf("raft persistence init error: %v", err)
	}
	raftNode.BindClientAddr(cfg.KVURL())
	raftNode.BindAddressBook(cfg.NodeKV, cfg.NodeRaft)
	go func() {
		if err := raftNode.Start(); err != nil {
			logger.Fatalf("raft server error: %v", err)
		}
	}()

	router := api.NewRouter(s, logger, cfg, raftNode)
	kvAddr := cfg.Addr()

	logger.Printf("KV Store server running on %s", cfg.KVURL())
	logger.Printf("Raft server running on %s", cfg.RaftURL())
	if err := http.ListenAndServe(kvAddr, router); err != nil {
		_ = raftNode.Stop(context.Background())
		logger.Fatalf("server error: %v", err)
	}
}
