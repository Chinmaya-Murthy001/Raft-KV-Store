package raft

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Transport struct {
	client *http.Client
}

func NewTransport() *Transport {
	return &Transport{
		client: &http.Client{Timeout: 500 * time.Millisecond},
	}
}

func (t *Transport) SendRequestVote(peer string, req RequestVoteRequest) (RequestVoteResponse, error) {
	var respBody RequestVoteResponse

	b, err := json.Marshal(req)
	if err != nil {
		return respBody, err
	}

	url := fmt.Sprintf("%s/raft/requestvote", peer)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return respBody, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return respBody, fmt.Errorf("requestvote status=%d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return respBody, err
	}
	return respBody, nil
}

func (t *Transport) SendAppendEntries(peer string, req AppendEntriesRequest) (AppendEntriesResponse, error) {
	var respBody AppendEntriesResponse

	b, err := json.Marshal(req)
	if err != nil {
		return respBody, err
	}

	url := fmt.Sprintf("%s/raft/appendentries", peer)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return respBody, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return respBody, fmt.Errorf("appendentries status=%d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return respBody, err
	}
	return respBody, nil
}
