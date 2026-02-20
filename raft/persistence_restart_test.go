package raft

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestP1FollowerRestartCatchesUp(t *testing.T) {
	baseDir := t.TempDir()
	followerDir := filepath.Join(baseDir, "n1")

	leaderKV := newFakeKV()
	followerKV := newFakeKV()
	otherFollowerKV := newFakeKV()

	leader := NewNode("n2", "9942", []string{"http://n1", "http://n3"}, nil)
	leader.BindStore(leaderKV)
	leader.mu.Lock()
	leader.currentTerm = 5
	leader.becomeLeaderLocked()
	leader.mu.Unlock()

	follower := NewNode("n1", "9941", nil, nil)
	follower.BindStore(followerKV)
	if err := follower.InitPersistence(followerDir); err != nil {
		t.Fatalf("InitPersistence follower error: %v", err)
	}

	otherFollower := NewNode("n3", "9943", nil, nil)
	otherFollower.BindStore(otherFollowerKV)

	initialWrites := []struct {
		key string
		val string
	}{
		{key: "a", val: "1"},
		{key: "b", val: "2"},
		{key: "c", val: "3"},
	}

	for _, w := range initialWrites {
		appendLeaderEntry(leader, Command{Op: OpSet, Key: w.key, Value: w.val})
		replicateAndApply(t, leader, "http://n1", follower)
		replicateAndApply(t, leader, "http://n3", otherFollower)
		// Ensure first follower receives updated leaderCommit after majority commit.
		replicateAndApply(t, leader, "http://n1", follower)
	}

	offlineWrites := []struct {
		key string
		val string
	}{
		{key: "d", val: "4"},
		{key: "e", val: "5"},
	}
	for _, w := range offlineWrites {
		appendLeaderEntry(leader, Command{Op: OpSet, Key: w.key, Value: w.val})
		replicateAndApply(t, leader, "http://n3", otherFollower)
	}

	restartedKV := newFakeKV()
	restartedFollower := NewNode("n1", "9941", nil, nil)
	restartedFollower.BindStore(restartedKV)
	if err := restartedFollower.InitPersistence(followerDir); err != nil {
		t.Fatalf("InitPersistence restarted follower error: %v", err)
	}

	replicateAndApply(t, leader, "http://n1", restartedFollower)

	for _, w := range append(initialWrites, offlineWrites...) {
		assertKVValue(t, leaderKV, w.key, w.val)
		assertKVValue(t, restartedKV, w.key, w.val)
	}
}

func TestP4NoDoubleApplyAcrossRestarts(t *testing.T) {
	baseDir := t.TempDir()
	followerDir := filepath.Join(baseDir, "n1")

	leaderKV := newFakeKV()
	firstFollowerKV := newFakeKV()

	leader := NewNode("n2", "9952", []string{"http://n1"}, nil)
	leader.BindStore(leaderKV)
	leader.mu.Lock()
	leader.currentTerm = 7
	leader.becomeLeaderLocked()
	leader.mu.Unlock()

	follower := NewNode("n1", "9951", nil, nil)
	follower.BindStore(firstFollowerKV)
	if err := follower.InitPersistence(followerDir); err != nil {
		t.Fatalf("InitPersistence follower error: %v", err)
	}

	appendLeaderEntry(leader, Command{Op: OpSet, Key: "x", Value: "1"})
	replicateAndApply(t, leader, "http://n1", follower)
	assertKVValue(t, firstFollowerKV, "x", "1")

	restart1KV := newFakeKV()
	restart1 := NewNode("n1", "9951", nil, nil)
	restart1.BindStore(restart1KV)
	if err := restart1.InitPersistence(followerDir); err != nil {
		t.Fatalf("InitPersistence restart1 error: %v", err)
	}
	replicateAndApply(t, leader, "http://n1", restart1)
	assertKVValue(t, restart1KV, "x", "1")

	appendLeaderEntry(leader, Command{Op: OpSet, Key: "x", Value: "2"})
	replicateAndApply(t, leader, "http://n1", restart1)
	assertKVValue(t, restart1KV, "x", "2")

	restart2KV := newFakeKV()
	restart2 := NewNode("n1", "9951", nil, nil)
	restart2.BindStore(restart2KV)
	if err := restart2.InitPersistence(followerDir); err != nil {
		t.Fatalf("InitPersistence restart2 error: %v", err)
	}
	replicateAndApply(t, leader, "http://n1", restart2)

	assertKVValue(t, leaderKV, "x", "2")
	assertKVValue(t, restart2KV, "x", "2")

	restart2.mu.RLock()
	if restart2.lastApplied != restart2.commitIndex {
		restart2.mu.RUnlock()
		t.Fatalf("restart2 lastApplied=%d commitIndex=%d", restart2.lastApplied, restart2.commitIndex)
	}
	restart2.mu.RUnlock()
}

func TestP2LeaderCrashAndRestartRejoinsAsFollower(t *testing.T) {
	baseDir := t.TempDir()
	n1Dir := filepath.Join(baseDir, "n1")
	n2Dir := filepath.Join(baseDir, "n2")
	n3Dir := filepath.Join(baseDir, "n3")

	n1KV := newFakeKV()
	n2KV := newFakeKV()
	n3KV := newFakeKV()

	n1 := NewNode("n1", "9961", []string{"http://n2", "http://n3"}, nil)
	n1.BindStore(n1KV)
	if err := n1.InitPersistence(n1Dir); err != nil {
		t.Fatalf("InitPersistence n1 error: %v", err)
	}
	n1.mu.Lock()
	n1.currentTerm = 1
	n1.becomeLeaderLocked()
	n1.mu.Unlock()

	n2 := NewNode("n2", "9962", []string{"http://n1", "http://n3"}, nil)
	n2.BindStore(n2KV)
	if err := n2.InitPersistence(n2Dir); err != nil {
		t.Fatalf("InitPersistence n2 error: %v", err)
	}

	n3 := NewNode("n3", "9963", []string{"http://n1", "http://n2"}, nil)
	n3.BindStore(n3KV)
	if err := n3.InitPersistence(n3Dir); err != nil {
		t.Fatalf("InitPersistence n3 error: %v", err)
	}

	// Write x=1 to leader L1 (n1) and replicate to followers.
	appendLeaderEntry(n1, Command{Op: OpSet, Key: "x", Value: "1"})
	replicateAndApply(t, n1, "http://n2", n2)
	replicateAndApply(t, n1, "http://n3", n3)
	replicateAndApply(t, n1, "http://n2", n2)

	// Kill L1; elect new leader L2 (n2).
	n2.mu.Lock()
	n2.currentTerm = 2
	n2.becomeLeaderLocked()
	n2.mu.Unlock()

	// Write y=2 to L2 and replicate to available follower n3.
	appendLeaderEntry(n2, Command{Op: OpSet, Key: "y", Value: "2"})
	replicateAndApply(t, n2, "http://n3", n3)

	// Restart old leader L1 from persisted state and catch it up from L2.
	restartedN1KV := newFakeKV()
	restartedN1 := NewNode("n1", "9961", nil, nil)
	restartedN1.BindStore(restartedN1KV)
	if err := restartedN1.InitPersistence(n1Dir); err != nil {
		t.Fatalf("InitPersistence restarted n1 error: %v", err)
	}
	replicateAndApply(t, n2, "http://n1", restartedN1)

	assertKVValue(t, n2KV, "x", "1")
	assertKVValue(t, n2KV, "y", "2")
	assertKVValue(t, restartedN1KV, "x", "1")
	assertKVValue(t, restartedN1KV, "y", "2")

	restartedN1.mu.RLock()
	if restartedN1.state != Follower {
		restartedN1.mu.RUnlock()
		t.Fatalf("restarted n1 state=%s want follower", restartedN1.state.String())
	}
	if restartedN1.currentTerm != 2 {
		restartedN1.mu.RUnlock()
		t.Fatalf("restarted n1 term=%d want 2", restartedN1.currentTerm)
	}
	restartedN1.mu.RUnlock()
}

func TestLeaderHintUpdatesAfterReelection(t *testing.T) {
	n := NewNode("n1", "9971", nil, nil)
	n.BindAddressBook(
		map[string]string{
			"n1": "http://localhost:8081",
			"n2": "http://localhost:8082",
			"n3": "http://localhost:8083",
		},
		map[string]string{
			"n1": "http://localhost:9081",
			"n2": "http://localhost:9082",
			"n3": "http://localhost:9083",
		},
	)
	n.BindClientAddr("http://localhost:8081")

	n.mu.Lock()
	n.currentTerm = 1
	n.becomeLeaderLocked()
	n.mu.Unlock()

	resp1 := n.HandleAppendEntries(AppendEntriesRequest{
		Term:         2,
		LeaderID:     "n2",
		LeaderAddr:   "http://localhost:8082",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		LeaderCommit: 0,
	})
	if !resp1.Success {
		t.Fatalf("append from n2 failed: %+v", resp1)
	}

	_, _, err := n.Propose(Command{Op: OpSet, Key: "k", Value: "1"})
	var notLeader *NotLeaderError
	if !errors.As(err, &notLeader) {
		t.Fatalf("expected NotLeaderError after n2 elected, got %v", err)
	}
	if notLeader.LeaderID != "n2" || notLeader.Leader != "http://localhost:8082" {
		t.Fatalf("unexpected leader hint after n2 election: %+v", notLeader)
	}

	resp2 := n.HandleAppendEntries(AppendEntriesRequest{
		Term:         3,
		LeaderID:     "n3",
		LeaderAddr:   "http://localhost:8083",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		LeaderCommit: 0,
	})
	if !resp2.Success {
		t.Fatalf("append from n3 failed: %+v", resp2)
	}

	_, _, err = n.Propose(Command{Op: OpSet, Key: "k2", Value: "2"})
	if !errors.As(err, &notLeader) {
		t.Fatalf("expected NotLeaderError after n3 elected, got %v", err)
	}
	if notLeader.LeaderID != "n3" || notLeader.Leader != "http://localhost:8083" {
		t.Fatalf("unexpected leader hint after n3 election: %+v", notLeader)
	}
}

func appendLeaderEntry(leader *Node, cmd Command) {
	leader.mu.Lock()
	entry := LogEntry{
		Term:    leader.currentTerm,
		Index:   leader.lastLogIndexLocked() + 1,
		Command: cmd,
	}
	leader.log = append(leader.log, entry)
	leader.persistLocked()
	leader.mu.Unlock()
}

func replicateAndApply(t *testing.T, leader *Node, peer string, follower *Node) {
	t.Helper()

	replicateUntilSettled(t, leader, peer, follower, 20)
	forceReplicationRound(leader, peer, follower)
	leader.applyCommittedEntries()
	follower.applyCommittedEntries()
}

func forceReplicationRound(leader *Node, peer string, follower *Node) {
	leader.mu.Lock()
	req, sentLen := leader.buildAppendEntriesForPeerLocked(peer)
	requestTerm := req.Term
	prevLogIndex := req.PrevLogIndex
	leader.mu.Unlock()

	resp := follower.HandleAppendEntries(req)

	leader.mu.Lock()
	leader.handleAppendResponseLocked(peer, requestTerm, prevLogIndex, sentLen, resp)
	leader.mu.Unlock()
}

func assertKVValue(t *testing.T, kv *fakeKV, key string, want string) {
	t.Helper()

	got, err := kv.Get(key)
	if err != nil {
		t.Fatalf("key %q read error: %v", key, err)
	}
	if got != want {
		t.Fatalf("key %q got=%q want=%q", key, got, want)
	}
}
