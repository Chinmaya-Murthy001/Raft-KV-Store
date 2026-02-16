package raft

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"
)

type Node struct {
	mu sync.Mutex

	id    string
	peers []string

	state         State
	currentTerm   int
	votedFor      string
	leaderID      string
	votesReceived int
	electionTerm  int

	electionTimeout  time.Duration
	electionDeadline time.Time
	lastHeartbeat    time.Time

	logger    *log.Logger
	transport *Transport
	runCtx    context.Context
}

func NewNode(id string, peers []string, logger *log.Logger) *Node {
	n := &Node{
		id:        id,
		peers:     peers,
		state:     Follower,
		logger:    logger,
		transport: NewTransport(),
	}
	n.mu.Lock()
	n.resetElectionTimerLocked()
	n.mu.Unlock()
	return n
}

func (n *Node) resetElectionTimerLocked() {
	// 600-1200ms randomized for more stable local demos on Windows.
	ms := 600 + rand.Intn(601) // 600..1200
	n.electionTimeout = time.Duration(ms) * time.Millisecond
	n.electionDeadline = time.Now().Add(n.electionTimeout)
}

func (n *Node) becomeFollowerLocked(term int) {
	n.state = Follower
	n.currentTerm = term
	n.votedFor = ""
	n.leaderID = ""
	n.votesReceived = 0
	n.electionTerm = 0
	n.resetElectionTimerLocked()
}

func (n *Node) startElectionLocked() (term int, candidateID string, peers []string, majority int) {
	n.state = Candidate
	n.currentTerm++
	n.electionTerm = n.currentTerm
	n.votedFor = n.id
	n.votesReceived = 1 // self-vote
	n.leaderID = ""
	n.resetElectionTimerLocked()

	total := len(n.peers) + 1
	majority = total/2 + 1

	if n.logger != nil {
		n.logger.Printf("[raft] %s ELECTION start term=%d votes=%d/%d", n.id, n.currentTerm, n.votesReceived, majority)
	}

	return n.currentTerm, n.id, append([]string{}, n.peers...), majority
}

func (n *Node) becomeLeaderLocked() {
	n.state = Leader
	n.leaderID = n.id
	n.votedFor = ""
	n.votesReceived = 0

	if n.logger != nil {
		n.logger.Printf("[raft] %s LEADER term=%d", n.id, n.currentTerm)
	}
}

func (n *Node) HandleRequestVote(req RequestVoteRequest) RequestVoteResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 1) stale term -> reject
	if req.Term < n.currentTerm {
		return RequestVoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	// 2) newer term -> step down
	if req.Term > n.currentTerm {
		n.becomeFollowerLocked(req.Term)
	}

	// 3) grant if not voted or voted same candidate
	if n.votedFor == "" || n.votedFor == req.CandidateID {
		n.votedFor = req.CandidateID
		n.resetElectionTimerLocked()
		return RequestVoteResponse{Term: n.currentTerm, VoteGranted: true}
	}

	return RequestVoteResponse{Term: n.currentTerm, VoteGranted: false}
}

func (n *Node) HandleAppendEntries(req AppendEntriesRequest) AppendEntriesResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	prevTerm := n.currentTerm
	prevLeader := n.leaderID

	if req.Term < n.currentTerm {
		return AppendEntriesResponse{Term: n.currentTerm, Success: false}
	}

	if req.Term > n.currentTerm {
		n.becomeFollowerLocked(req.Term)
	}

	// req.Term == currentTerm here.
	// Accept leader, step down if needed, reset timer.
	n.leaderID = req.LeaderID
	if n.state != Follower {
		n.state = Follower
		n.votedFor = ""
		n.votesReceived = 0
		n.electionTerm = 0
	}
	n.lastHeartbeat = time.Now()
	n.resetElectionTimerLocked()
	if n.logger != nil && (prevTerm != n.currentTerm || prevLeader != req.LeaderID) {
		n.logger.Printf("[raft] %s HB from %s term=%d", n.id, req.LeaderID, req.Term)
	}

	return AppendEntriesResponse{Term: n.currentTerm, Success: true}
}

func (n *Node) requestVotes(term int, cid string, peers []string, majority int) {
	req := RequestVoteRequest{Term: term, CandidateID: cid}

	for _, p := range peers {
		peer := p
		go func() {
			resp, err := n.transport.SendRequestVote(peer, req)
			if err != nil {
				if n.logger != nil {
					n.logger.Printf("[raft] %s VOTE request to %s error=%v", cid, peer, err)
				}
				return
			}

			n.mu.Lock()

			// if peer has higher term -> step down
			if resp.Term > n.currentTerm {
				n.becomeFollowerLocked(resp.Term)
				if n.logger != nil {
					n.logger.Printf("[raft] %s STEPDOWN higher-term=%d", n.id, resp.Term)
				}
				n.mu.Unlock()
				return
			}

			// Only count votes for the active election term and state.
			if n.state != Candidate || n.currentTerm != term || n.electionTerm != term {
				n.mu.Unlock()
				return
			}

			if resp.VoteGranted {
				n.votesReceived++
				won := false
				ctx := n.runCtx
				leaderTerm := n.currentTerm

				if n.logger != nil {
					n.logger.Printf("[raft] %s VOTE from %s votes=%d/%d term=%d", n.id, peer, n.votesReceived, majority, n.currentTerm)
				}
				if n.votesReceived >= majority {
					n.becomeLeaderLocked()
					won = true
					leaderTerm = n.currentTerm
				}
				n.mu.Unlock()
				if won && ctx != nil {
					go n.heartbeatLoop(ctx, leaderTerm)
				}
				return
			}

			if n.logger != nil {
				n.logger.Printf("[raft] %s VOTE denied by %s term=%d", cid, peer, resp.Term)
			}
			n.mu.Unlock()
		}()
	}
}

func (n *Node) heartbeatLoop(ctx context.Context, leaderTerm int) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.mu.Lock()
			if n.state != Leader || n.currentTerm != leaderTerm {
				n.mu.Unlock()
				return
			}
			term := n.currentTerm
			peers := append([]string{}, n.peers...)
			n.mu.Unlock()

			req := AppendEntriesRequest{Term: term, LeaderID: n.id}
			for _, p := range peers {
				peer := p
				go func() {
					resp, err := n.transport.SendAppendEntries(peer, req)
					if err != nil {
						return
					}

					n.mu.Lock()
					defer n.mu.Unlock()
					if resp.Term > n.currentTerm {
						n.becomeFollowerLocked(resp.Term)
						if n.logger != nil {
							n.logger.Printf("[raft] %s STEPDOWN heartbeat higher-term=%d", n.id, resp.Term)
						}
					}
				}()
			}
		}
	}
}

func (n *Node) Run(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	n.mu.Lock()
	n.runCtx = ctx
	n.mu.Unlock()

	if n.logger != nil {
		n.logger.Printf("[raft] %s started state=%s", n.id, n.state.String())
	}

	for {
		select {
		case <-ctx.Done():
			if n.logger != nil {
				n.logger.Printf("[raft] %s stopping", n.id)
			}
			return

		case <-ticker.C:
			n.mu.Lock()
			// For Day 2: only follower timeout triggers election
			if n.state != Leader && time.Now().After(n.electionDeadline) {
				term, cid, peers, majority := n.startElectionLocked()
				if n.votesReceived >= majority {
					n.becomeLeaderLocked()
					n.mu.Unlock()
					go n.heartbeatLoop(ctx, term)
					continue
				}
				n.mu.Unlock()
				n.requestVotes(term, cid, peers, majority)
				continue
			}
			n.mu.Unlock()
		}
	}
}

// Status snapshot for /health.
type Status struct {
	ID                 string `json:"id"`
	State              string `json:"state"`
	Term               int    `json:"term"`
	LeaderID           string `json:"leaderId"`
	PeersCount         int    `json:"peersCount"`
	LastHeartbeatAgoMs int64  `json:"lastHeartbeatAgoMs"`
}

func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()

	lastHeartbeatAgo := int64(-1)
	if !n.lastHeartbeat.IsZero() {
		lastHeartbeatAgo = time.Since(n.lastHeartbeat).Milliseconds()
	}

	return Status{
		ID:                 n.id,
		State:              n.state.String(),
		Term:               n.currentTerm,
		LeaderID:           n.leaderID,
		PeersCount:         len(n.peers),
		LastHeartbeatAgoMs: lastHeartbeatAgo,
	}
}
