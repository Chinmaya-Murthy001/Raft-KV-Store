package raft

type RequestVoteRequest struct {
	Term        int    `json:"term"`
	CandidateID string `json:"candidateId"`
}

type RequestVoteResponse struct {
	Term        int  `json:"term"`
	VoteGranted bool `json:"voteGranted"`
}

type AppendEntriesRequest struct {
	Term     int    `json:"term"`
	LeaderID string `json:"leaderId"`
}

type AppendEntriesResponse struct {
	Term    int  `json:"term"`
	Success bool `json:"success"`
}
