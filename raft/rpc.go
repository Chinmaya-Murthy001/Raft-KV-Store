package raft

type RequestVoteRequest struct {
	Term         int    `json:"term"`
	CandidateID  string `json:"candidateId"`
	LastLogIndex int    `json:"lastLogIndex"`
	LastLogTerm  int    `json:"lastLogTerm"`
}

type RequestVoteResponse struct {
	Term        int  `json:"term"`
	VoteGranted bool `json:"voteGranted"`
}

type AppendEntriesRequest struct {
	Term         int        `json:"term"`
	LeaderID     string     `json:"leaderId"`
	LeaderAddr   string     `json:"leaderAddr,omitempty"`
	PrevLogIndex int        `json:"prevLogIndex"`
	PrevLogTerm  int        `json:"prevLogTerm"`
	Entries      []LogEntry `json:"entries"`
	LeaderCommit int        `json:"leaderCommit"`
}

type AppendEntriesResponse struct {
	Term    int  `json:"term"`
	Success bool `json:"success"`
}
