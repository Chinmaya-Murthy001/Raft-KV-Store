package api

import (
	"encoding/json"
	"net/http"
	"time"

	"kvstore/raft"
)

type healthResponse struct {
	ID                 string   `json:"id"`
	Addr               string   `json:"addr"`
	Peers              []string `json:"peers"`
	PeersCount         int      `json:"peersCount"`
	State              string   `json:"state"`
	Term               int      `json:"term"`
	LeaderID           string   `json:"leaderId"`
	Leader             string   `json:"leader,omitempty"`
	LastHeartbeatAgoMs int64    `json:"lastHeartbeatAgoMs"`
}

func (r *Router) Health(w http.ResponseWriter, req *http.Request) {
	start := time.Now()

	if req.Method != http.MethodGet {
		status := http.StatusMethodNotAllowed
		writeJSON(w, status, apiResponse{OK: false, Message: "method not allowed"})
		r.handler.logRequest(req, start, status)
		return
	}

	status := http.StatusOK
	state := "unknown"
	term := 0
	leaderID := ""
	leader := ""
	peersCount := len(r.cfg.Peers)
	lastHeartbeatAgoMs := int64(-1)
	if r.raft != nil {
		snap := r.raft.Status()
		peersCount = snap.PeersCount
		state = snap.State
		term = snap.Term
		leaderID = snap.LeaderID
		leader = snap.LeaderAddr
		lastHeartbeatAgoMs = snap.LastHeartbeatAgoMs
	}

	resp := healthResponse{
		ID:                 r.cfg.NodeID,
		Addr:               "http://localhost" + r.cfg.Addr(),
		Peers:              r.cfg.Peers,
		PeersCount:         peersCount,
		State:              state,
		Term:               term,
		LeaderID:           leaderID,
		Leader:             leader,
		LastHeartbeatAgoMs: lastHeartbeatAgoMs,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
	r.handler.logRequest(req, start, status)
}

func (r *Router) RequestVote(w http.ResponseWriter, req *http.Request) {
	start := time.Now()

	if req.Method != http.MethodPost {
		status := http.StatusMethodNotAllowed
		writeJSON(w, status, apiResponse{OK: false, Message: "method not allowed"})
		r.handler.logRequest(req, start, status)
		return
	}

	if r.raft == nil {
		status := http.StatusInternalServerError
		writeJSON(w, status, apiResponse{OK: false, Message: "raft not initialized"})
		r.handler.logRequest(req, start, status)
		return
	}

	defer req.Body.Close()

	var voteReq raft.RequestVoteRequest
	if err := json.NewDecoder(req.Body).Decode(&voteReq); err != nil {
		status := http.StatusBadRequest
		writeJSON(w, status, apiResponse{OK: false, Message: "invalid JSON body"})
		r.handler.logRequest(req, start, status)
		return
	}

	voteResp := r.raft.HandleRequestVote(voteReq)
	status := http.StatusOK
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(voteResp)
	r.handler.logRequest(req, start, status)
}

func (r *Router) AppendEntries(w http.ResponseWriter, req *http.Request) {
	start := time.Now()

	if req.Method != http.MethodPost {
		status := http.StatusMethodNotAllowed
		writeJSON(w, status, apiResponse{OK: false, Message: "method not allowed"})
		r.handler.logRequest(req, start, status)
		return
	}

	if r.raft == nil {
		status := http.StatusInternalServerError
		writeJSON(w, status, apiResponse{OK: false, Message: "raft not initialized"})
		r.handler.logRequest(req, start, status)
		return
	}

	defer req.Body.Close()

	var appendReq raft.AppendEntriesRequest
	if err := json.NewDecoder(req.Body).Decode(&appendReq); err != nil {
		status := http.StatusBadRequest
		writeJSON(w, status, apiResponse{OK: false, Message: "invalid JSON body"})
		r.handler.logRequest(req, start, status)
		return
	}

	appendResp := r.raft.HandleAppendEntries(appendReq)
	status := http.StatusOK
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(appendResp)
	r.handler.logRequest(req, start, status)
}
