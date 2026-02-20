package raft

import "testing"

func TestHigherTermForcesStepDownInAppendEntriesHandler(t *testing.T) {
	n := NewNode("n1", "9981", nil, nil)

	n.mu.Lock()
	n.currentTerm = 4
	n.state = Leader
	n.votedFor = "n1"
	n.mu.Unlock()

	resp := n.HandleAppendEntries(AppendEntriesRequest{
		Term:         5,
		LeaderID:     "n2",
		LeaderAddr:   "http://localhost:8082",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		LeaderCommit: 0,
	})
	if !resp.Success {
		t.Fatalf("expected success for higher-term append, got %+v", resp)
	}

	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.currentTerm != 5 {
		t.Fatalf("currentTerm=%d want=5", n.currentTerm)
	}
	if n.state != Follower {
		t.Fatalf("state=%s want=follower", n.state.String())
	}
	if n.votedFor != "" {
		t.Fatalf("votedFor=%q want empty", n.votedFor)
	}
}

func TestHigherTermForcesStepDownInAppendReplyHandling(t *testing.T) {
	n := NewNode("n1", "9982", []string{"http://p1"}, nil)

	n.mu.Lock()
	n.currentTerm = 4
	n.state = Leader
	n.votedFor = "n1"
	n.mu.Unlock()

	n.mu.Lock()
	n.handleAppendResponseLocked("http://p1", 4, 0, 0, AppendEntriesResponse{
		Term:    6,
		Success: false,
	})
	n.mu.Unlock()

	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.currentTerm != 6 {
		t.Fatalf("currentTerm=%d want=6", n.currentTerm)
	}
	if n.state != Follower {
		t.Fatalf("state=%s want=follower", n.state.String())
	}
	if n.votedFor != "" {
		t.Fatalf("votedFor=%q want empty", n.votedFor)
	}
}
