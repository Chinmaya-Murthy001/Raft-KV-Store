package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"kvstore/raft"
	"kvstore/store"
	"kvstore/utils"
)

type Handler struct {
	Store  store.Store
	Logger *utils.Logger
	Raft   *raft.Node
}

type setRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type apiResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Message  string `json:"message,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	LeaderID string `json:"leaderId,omitempty"`
	Leader   string `json:"leader,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, payload apiResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) logRequest(r *http.Request, start time.Time, status int) {
	if h.Logger == nil {
		return
	}
	reqID := requestIDFromContext(r.Context())
	h.Logger.Printf("[%s] %s %s -> %d (%s)", reqID, r.Method, r.URL.Path, status, time.Since(start))
}

func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodPut {
		status := http.StatusMethodNotAllowed
		writeJSON(w, status, apiResponse{OK: false, Message: "method not allowed"})
		h.logRequest(r, start, status)
		return
	}

	defer r.Body.Close()

	var req setRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		status := http.StatusBadRequest
		writeJSON(w, status, apiResponse{OK: false, Message: "invalid JSON body"})
		h.logRequest(r, start, status)
		return
	}

	if h.Raft != nil {
		_, _, err := h.Raft.Propose(raft.Command{
			Op:    raft.OpSet,
			Key:   req.Key,
			Value: req.Value,
		})
		if err != nil {
			status := http.StatusInternalServerError
			msg := "internal server error"
			errCode := "internal_error"
			leaderHint := ""
			leaderID := ""

			var notLeader *raft.NotLeaderError
			if errors.As(err, &notLeader) {
				status = http.StatusConflict
				msg = "not leader"
				errCode = "not_leader"
				if notLeader != nil {
					leaderHint = notLeader.Leader
					leaderID = notLeader.LeaderID
				}
			} else if errors.Is(err, raft.ErrProposalTimeout) {
				status = http.StatusGatewayTimeout
				msg = "proposal timeout"
				errCode = "timeout"
			} else if errors.Is(err, store.ErrKeyEmpty) {
				status = http.StatusBadRequest
				msg = err.Error()
				errCode = "bad_request"
			}

			writeJSON(w, status, apiResponse{OK: false, Error: errCode, Message: msg, LeaderID: leaderID, Leader: leaderHint})
			h.logRequest(r, start, status)
			return
		}
	} else {
		if err := h.Store.Set(req.Key, req.Value); err != nil {
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.Is(err, store.ErrKeyEmpty) {
				status = http.StatusBadRequest
				msg = err.Error()
			}
			writeJSON(w, status, apiResponse{OK: false, Message: msg})
			h.logRequest(r, start, status)
			return
		}
	}

	status := http.StatusOK
	writeJSON(w, status, apiResponse{OK: true, Message: "stored", Key: req.Key})
	h.logRequest(r, start, status)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodGet {
		status := http.StatusMethodNotAllowed
		writeJSON(w, status, apiResponse{OK: false, Message: "method not allowed"})
		h.logRequest(r, start, status)
		return
	}

	key := r.URL.Query().Get("key")
	val, err := h.Store.Get(key)

	if err != nil {
		if errors.Is(err, store.ErrKeyNotFound) {
			status := http.StatusNotFound
			writeJSON(w, status, apiResponse{OK: false, Message: "key not found"})
			h.logRequest(r, start, status)
			return
		}
		status := http.StatusInternalServerError
		msg := "internal server error"
		if errors.Is(err, store.ErrKeyEmpty) {
			status = http.StatusBadRequest
			msg = err.Error()
		}
		writeJSON(w, status, apiResponse{OK: false, Message: msg})
		h.logRequest(r, start, status)
		return
	}

	status := http.StatusOK
	writeJSON(w, status, apiResponse{OK: true, Key: key, Value: val})
	h.logRequest(r, start, status)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodDelete {
		status := http.StatusMethodNotAllowed
		writeJSON(w, status, apiResponse{OK: false, Message: "method not allowed"})
		h.logRequest(r, start, status)
		return
	}

	key := r.URL.Query().Get("key")
	var err error
	if h.Raft != nil {
		_, _, err = h.Raft.Propose(raft.Command{
			Op:  raft.OpDelete,
			Key: key,
		})
	} else {
		err = h.Store.Delete(key)
	}

	if err != nil {
		var notLeader *raft.NotLeaderError
		if errors.As(err, &notLeader) {
			status := http.StatusConflict
			leaderHint := ""
			leaderID := ""
			if notLeader != nil {
				leaderHint = notLeader.Leader
				leaderID = notLeader.LeaderID
			}
			writeJSON(w, status, apiResponse{OK: false, Error: "not_leader", Message: "not leader", LeaderID: leaderID, Leader: leaderHint})
			h.logRequest(r, start, status)
			return
		}
		if errors.Is(err, raft.ErrProposalTimeout) {
			status := http.StatusGatewayTimeout
			writeJSON(w, status, apiResponse{OK: false, Error: "timeout", Message: "proposal timeout"})
			h.logRequest(r, start, status)
			return
		}
		if errors.Is(err, store.ErrKeyNotFound) {
			status := http.StatusNotFound
			writeJSON(w, status, apiResponse{OK: false, Message: "key not found"})
			h.logRequest(r, start, status)
			return
		}
		status := http.StatusInternalServerError
		msg := "internal server error"
		if errors.Is(err, store.ErrKeyEmpty) {
			status = http.StatusBadRequest
			msg = err.Error()
		}
		writeJSON(w, status, apiResponse{OK: false, Message: msg})
		h.logRequest(r, start, status)
		return
	}

	status := http.StatusOK
	writeJSON(w, status, apiResponse{OK: true, Message: "deleted", Key: key})
	h.logRequest(r, start, status)
}
