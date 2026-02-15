package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"kvstore/store"
	"kvstore/utils"
)

type Handler struct {
	Store  store.Store
	Logger *utils.Logger
}

type setRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type apiResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
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
	err := h.Store.Delete(key)

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
	writeJSON(w, status, apiResponse{OK: true, Message: "deleted", Key: key})
	h.logRequest(r, start, status)
}
