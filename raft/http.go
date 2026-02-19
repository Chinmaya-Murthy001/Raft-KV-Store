package raft

import (
	"encoding/json"
	"net/http"
)

type routeError struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (n *Node) registerHTTPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/raft/status", n.handleStatus)
	mux.HandleFunc("/raft/appendentries", n.handleAppendEntries)
	mux.HandleFunc("/raft/requestvote", n.handleRequestVote)
}

func (n *Node) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeRouteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(n.Status())
}

func (n *Node) handleAppendEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRouteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer r.Body.Close()

	var req AppendEntriesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	resp := n.HandleAppendEntries(req)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (n *Node) handleRequestVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRouteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer r.Body.Close()

	var req RequestVoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	resp := n.HandleRequestVote(req)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeRouteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(routeError{
		OK:      false,
		Message: msg,
	})
}
