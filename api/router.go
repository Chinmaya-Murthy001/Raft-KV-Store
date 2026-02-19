package api

import (
	"net/http"

	"kvstore/config"
	"kvstore/raft"
	"kvstore/store"
	"kvstore/utils"
)

type Router struct {
	handler *Handler
	cfg     config.Config
	raft    *raft.Node
}

// NewRouter builds the API routes and applies common middleware.
func NewRouter(s store.Store, logger *utils.Logger, cfg config.Config, raftNode *raft.Node) http.Handler {
	h := &Handler{
		Store:  s,
		Logger: logger,
		Raft:   raftNode,
	}
	r := &Router{
		handler: h,
		cfg:     cfg,
		raft:    raftNode,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/set", r.handler.Set)
	mux.HandleFunc("/get", r.handler.Get)
	mux.HandleFunc("/delete", r.handler.Delete)
	mux.HandleFunc("/health", r.Health)

	var root http.Handler = mux
	root = panicRecoveryMiddleware(root, logger)
	root = requestIDMiddleware(root)
	return root
}
