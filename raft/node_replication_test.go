package raft

import "testing"

func TestBecomeLeaderInitializesReplicationState(t *testing.T) {
	n := NewNode("n1", "9901", []string{"http://p1", "http://p2"}, nil)

	n.mu.Lock()
	n.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
		{Index: 3, Term: 2},
	}
	n.becomeLeaderLocked()
	n.mu.Unlock()

	n.mu.RLock()
	defer n.mu.RUnlock()

	if n.nextIndex["http://p1"] != 4 || n.nextIndex["http://p2"] != 4 {
		t.Fatalf("nextIndex not initialized to lastLogIndex+1: got p1=%d p2=%d", n.nextIndex["http://p1"], n.nextIndex["http://p2"])
	}
	if n.matchIndex["http://p1"] != 0 || n.matchIndex["http://p2"] != 0 {
		t.Fatalf("matchIndex not initialized to 0: got p1=%d p2=%d", n.matchIndex["http://p1"], n.matchIndex["http://p2"])
	}
}

func TestBuildAppendEntriesForPeer(t *testing.T) {
	n := NewNode("n1", "9902", []string{"http://p1"}, nil)

	n.mu.Lock()
	n.currentTerm = 5
	n.state = Leader
	n.commitIndex = 2
	n.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 1},
		{Index: 3, Term: 2},
	}
	n.nextIndex["http://p1"] = 2
	req, sentLen := n.buildAppendEntriesForPeerLocked("http://p1")
	n.mu.Unlock()

	if req.PrevLogIndex != 1 {
		t.Fatalf("PrevLogIndex=%d want 1", req.PrevLogIndex)
	}
	if req.PrevLogTerm != 1 {
		t.Fatalf("PrevLogTerm=%d want 1", req.PrevLogTerm)
	}
	if sentLen != 2 || len(req.Entries) != 2 {
		t.Fatalf("entries len=%d sent=%d want 2", len(req.Entries), sentLen)
	}
	if req.Entries[0].Index != 2 || req.Entries[1].Index != 3 {
		t.Fatalf("unexpected entries indexes: %+v", req.Entries)
	}
}

func TestHandleAppendResponseUpdatesNextAndMatch(t *testing.T) {
	n := NewNode("n1", "9903", []string{"http://p1"}, nil)

	n.mu.Lock()
	n.currentTerm = 3
	n.state = Leader
	n.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 2},
		{Index: 3, Term: 3},
	}
	n.nextIndex["http://p1"] = 2
	n.matchIndex["http://p1"] = 0

	n.handleAppendResponseLocked("http://p1", 3, 1, 2, AppendEntriesResponse{Term: 3, Success: true})
	if n.matchIndex["http://p1"] != 3 {
		t.Fatalf("matchIndex=%d want 3", n.matchIndex["http://p1"])
	}
	if n.nextIndex["http://p1"] != 4 {
		t.Fatalf("nextIndex=%d want 4", n.nextIndex["http://p1"])
	}
	if n.commitIndex != 3 {
		t.Fatalf("commitIndex=%d want 3", n.commitIndex)
	}

	n.handleAppendResponseLocked("http://p1", 3, 3, 0, AppendEntriesResponse{Term: 3, Success: false})
	if n.nextIndex["http://p1"] != 3 {
		t.Fatalf("nextIndex after fail=%d want 3", n.nextIndex["http://p1"])
	}
	n.handleAppendResponseLocked("http://p1", 3, 2, 0, AppendEntriesResponse{Term: 3, Success: false})
	n.handleAppendResponseLocked("http://p1", 3, 1, 0, AppendEntriesResponse{Term: 3, Success: false})
	n.handleAppendResponseLocked("http://p1", 3, 0, 0, AppendEntriesResponse{Term: 3, Success: false})
	if n.nextIndex["http://p1"] != 1 {
		t.Fatalf("nextIndex floor=%d want 1", n.nextIndex["http://p1"])
	}
	n.mu.Unlock()
}

func TestAdvanceCommitIndexRequiresCurrentTerm(t *testing.T) {
	n := NewNode("n1", "9904", []string{"http://p1", "http://p2"}, nil)

	n.mu.Lock()
	n.state = Leader
	n.currentTerm = 3
	n.log = []LogEntry{
		{Index: 1, Term: 1},
		{Index: 2, Term: 2},
	}
	n.matchIndex["http://p1"] = 2
	n.matchIndex["http://p2"] = 2

	n.advanceCommitIndexLocked()
	if n.commitIndex != 0 {
		t.Fatalf("commitIndex=%d want 0 (old-term entries must not be newly committed)", n.commitIndex)
	}

	n.log = append(n.log, LogEntry{Index: 3, Term: 3})
	n.matchIndex["http://p1"] = 3
	n.matchIndex["http://p2"] = 2
	n.advanceCommitIndexLocked()
	if n.commitIndex != 3 {
		t.Fatalf("commitIndex=%d want 3 once current-term majority exists", n.commitIndex)
	}
	n.mu.Unlock()
}
