package raft

import "testing"

func TestLaggingFollowerCatchUp(t *testing.T) {
	leader := NewNode("leader", "9911", []string{"http://f2"}, nil)
	follower2 := NewNode("f2", "9912", nil, nil)

	leader.mu.Lock()
	leader.state = Leader
	leader.currentTerm = 3
	leader.log = []LogEntry{
		{Index: 1, Term: 1, Command: Command{Op: OpSet, Key: "k1", Value: "v1"}},
		{Index: 2, Term: 1, Command: Command{Op: OpSet, Key: "k2", Value: "v2"}},
		{Index: 3, Term: 2, Command: Command{Op: OpSet, Key: "k3", Value: "v3"}},
		{Index: 4, Term: 2, Command: Command{Op: OpSet, Key: "k4", Value: "v4"}},
		{Index: 5, Term: 3, Command: Command{Op: OpSet, Key: "k5", Value: "v5"}},
	}
	leader.nextIndex["http://f2"] = leader.lastLogIndexLocked() + 1
	leader.matchIndex["http://f2"] = 0
	leader.mu.Unlock()

	follower2.mu.Lock()
	follower2.currentTerm = 2
	// Simulate restarted lagging follower with old persisted prefix only.
	follower2.log = []LogEntry{
		{Index: 1, Term: 1, Command: Command{Op: OpSet, Key: "k1", Value: "v1"}},
		{Index: 2, Term: 1, Command: Command{Op: OpSet, Key: "k2", Value: "v2"}},
	}
	follower2.mu.Unlock()

	replicateUntilSettled(t, leader, "http://f2", follower2, 12)

	leader.mu.RLock()
	defer leader.mu.RUnlock()
	follower2.mu.RLock()
	defer follower2.mu.RUnlock()

	if len(follower2.log) != len(leader.log) {
		t.Fatalf("follower log len=%d want=%d", len(follower2.log), len(leader.log))
	}
	for i := range leader.log {
		if follower2.log[i].Index != leader.log[i].Index || follower2.log[i].Term != leader.log[i].Term {
			t.Fatalf("log mismatch at i=%d follower=%+v leader=%+v", i, follower2.log[i], leader.log[i])
		}
	}
	if leader.nextIndex["http://f2"] != leader.lastLogIndexLocked()+1 {
		t.Fatalf("nextIndex=%d want=%d", leader.nextIndex["http://f2"], leader.lastLogIndexLocked()+1)
	}
	if leader.matchIndex["http://f2"] != leader.lastLogIndexLocked() {
		t.Fatalf("matchIndex=%d want=%d", leader.matchIndex["http://f2"], leader.lastLogIndexLocked())
	}
}

func TestConflictingTailOverwrite(t *testing.T) {
	leader := NewNode("new-leader", "9921", []string{"http://old-leader"}, nil)
	oldLeaderNowFollower := NewNode("old-leader", "9922", nil, nil)

	leader.mu.Lock()
	leader.state = Leader
	leader.currentTerm = 4
	leader.log = []LogEntry{
		{Index: 1, Term: 1, Command: Command{Op: OpSet, Key: "a", Value: "1"}},
		{Index: 2, Term: 1, Command: Command{Op: OpSet, Key: "b", Value: "1"}},
		{Index: 3, Term: 3, Command: Command{Op: OpSet, Key: "c", Value: "leader"}},
		{Index: 4, Term: 3, Command: Command{Op: OpSet, Key: "d", Value: "leader"}},
	}
	leader.nextIndex["http://old-leader"] = leader.lastLogIndexLocked() + 1
	leader.matchIndex["http://old-leader"] = 0
	leader.mu.Unlock()

	oldLeaderNowFollower.mu.Lock()
	oldLeaderNowFollower.currentTerm = 3
	oldLeaderNowFollower.state = Leader
	// Divergent uncommitted tail from old leader.
	oldLeaderNowFollower.log = []LogEntry{
		{Index: 1, Term: 1, Command: Command{Op: OpSet, Key: "a", Value: "1"}},
		{Index: 2, Term: 1, Command: Command{Op: OpSet, Key: "b", Value: "1"}},
		{Index: 3, Term: 2, Command: Command{Op: OpSet, Key: "x", Value: "old"}},
		{Index: 4, Term: 2, Command: Command{Op: OpSet, Key: "y", Value: "old"}},
	}
	oldLeaderNowFollower.mu.Unlock()

	replicateUntilSettled(t, leader, "http://old-leader", oldLeaderNowFollower, 12)

	leader.mu.RLock()
	defer leader.mu.RUnlock()
	oldLeaderNowFollower.mu.RLock()
	defer oldLeaderNowFollower.mu.RUnlock()

	if len(oldLeaderNowFollower.log) != len(leader.log) {
		t.Fatalf("follower log len=%d want=%d", len(oldLeaderNowFollower.log), len(leader.log))
	}
	for i := range leader.log {
		if oldLeaderNowFollower.log[i].Index != leader.log[i].Index || oldLeaderNowFollower.log[i].Term != leader.log[i].Term {
			t.Fatalf("log mismatch at i=%d follower=%+v leader=%+v", i, oldLeaderNowFollower.log[i], leader.log[i])
		}
	}
	if oldLeaderNowFollower.state != Follower {
		t.Fatalf("old leader did not step down; state=%s", oldLeaderNowFollower.state.String())
	}
}

func replicateUntilSettled(t *testing.T, leader *Node, peer string, follower *Node, maxRounds int) {
	t.Helper()

	for i := 0; i < maxRounds; i++ {
		leader.mu.Lock()
		req, sentLen := leader.buildAppendEntriesForPeerLocked(peer)
		requestTerm := req.Term
		prevLogIndex := req.PrevLogIndex
		leader.mu.Unlock()

		resp := follower.HandleAppendEntries(req)

		leader.mu.Lock()
		_ = leader.handleAppendResponseLocked(peer, requestTerm, prevLogIndex, sentLen, resp)
		leaderLast := leader.lastLogIndexLocked()
		peerMatch := leader.matchIndex[peer]
		leader.mu.Unlock()

		if peerMatch >= leaderLast {
			return
		}
	}

	leader.mu.RLock()
	last := leader.lastLogIndexLocked()
	match := leader.matchIndex[peer]
	leader.mu.RUnlock()
	t.Fatalf("replication did not settle; matchIndex=%d lastLogIndex=%d", match, last)
}
