package main

import (
	"context"
	"math/rand"
	"net/http"
	"time"

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
	rand.Seed(time.Now().UnixNano())

	raftNode := raft.NewNode(cfg.NodeID, cfg.Peers, logger.Logger)
	raftCtx, raftCancel := context.WithCancel(context.Background())
	defer raftCancel()
	go raftNode.Run(raftCtx)

	router := api.NewRouter(s, logger, cfg, raftNode)
	addr := cfg.Addr()

	logger.Printf("KV Store server running on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		raftCancel()
		logger.Fatalf("server error: %v", err)
	}
}
