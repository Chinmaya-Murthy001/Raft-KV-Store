package main

import (
	"net/http"

	"kvstore/api"
	"kvstore/config"
	"kvstore/store"
	"kvstore/utils"
)

func main() {
	cfg := config.Load()
	logger := utils.NewLogger()
	s := store.NewDefaultStore()

	h := &api.Handler{
		Store:  s,
		Logger: logger,
	}

	router := api.NewRouter(h)
	addr := cfg.Addr()

	logger.Printf("KV Store server running on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		logger.Fatalf("server error: %v", err)
	}
}
