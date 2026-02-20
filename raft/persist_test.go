package raft

import (
	"reflect"
	"testing"
)

func TestPersisterSaveLoadRoundtrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := NewPersister(dir)

	want := StableState{
		CurrentTerm: 7,
		VotedFor:    "n2",
		Log: []LogEntry{
			{
				Term:  6,
				Index: 1,
				Command: Command{
					Op:    OpSet,
					Key:   "k1",
					Value: "v1",
				},
			},
			{
				Term:  7,
				Index: 2,
				Command: Command{
					Op:  OpDelete,
					Key: "k1",
				},
			},
		},
	}

	if err := p.Save(want); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	got, ok, err := p.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !ok {
		t.Fatal("Load() returned ok=false, expected persisted state")
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestHandlersPersistTermVoteAndLogMutations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	n := NewNode("n1", "9909", nil, nil)
	if err := n.InitPersistence(dir); err != nil {
		t.Fatalf("InitPersistence() error: %v", err)
	}

	voteResp := n.HandleRequestVote(RequestVoteRequest{
		Term:         2,
		CandidateID:  "n2",
		LastLogIndex: 0,
		LastLogTerm:  0,
	})
	if !voteResp.VoteGranted {
		t.Fatalf("expected vote granted, got %+v", voteResp)
	}

	p := NewPersister(dir)
	afterVote, ok, err := p.Load()
	if err != nil {
		t.Fatalf("Load() after vote error: %v", err)
	}
	if !ok {
		t.Fatal("Load() after vote returned ok=false")
	}
	if afterVote.CurrentTerm != 2 {
		t.Fatalf("after vote currentTerm=%d want=2", afterVote.CurrentTerm)
	}
	if afterVote.VotedFor != "n2" {
		t.Fatalf("after vote votedFor=%q want=%q", afterVote.VotedFor, "n2")
	}

	appendResp := n.HandleAppendEntries(AppendEntriesRequest{
		Term:         3,
		LeaderID:     "n3",
		LeaderAddr:   "http://localhost:8083",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries: []LogEntry{
			{
				Term:  3,
				Index: 1,
				Command: Command{
					Op:    OpSet,
					Key:   "name",
					Value: "luffy",
				},
			},
		},
		LeaderCommit: 0,
	})
	if !appendResp.Success {
		t.Fatalf("expected append success, got %+v", appendResp)
	}

	afterAppend, ok, err := p.Load()
	if err != nil {
		t.Fatalf("Load() after append error: %v", err)
	}
	if !ok {
		t.Fatal("Load() after append returned ok=false")
	}
	if afterAppend.CurrentTerm != 3 {
		t.Fatalf("after append currentTerm=%d want=3", afterAppend.CurrentTerm)
	}
	if afterAppend.VotedFor != "" {
		t.Fatalf("after append votedFor=%q want empty", afterAppend.VotedFor)
	}
	if len(afterAppend.Log) != 1 {
		t.Fatalf("after append log len=%d want=1", len(afterAppend.Log))
	}
	if afterAppend.Log[0].Index != 1 || afterAppend.Log[0].Term != 3 {
		t.Fatalf("after append log[0]=%+v want index=1 term=3", afterAppend.Log[0])
	}
	if afterAppend.Log[0].Command.Op != OpSet ||
		afterAppend.Log[0].Command.Key != "name" ||
		afterAppend.Log[0].Command.Value != "luffy" {
		t.Fatalf("after append command=%+v unexpected", afterAppend.Log[0].Command)
	}
}

func TestPersisterSaveLoadSnapshotRoundtrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := NewPersister(dir)

	want := Snapshot{
		LastIncludedIndex: 42,
		LastIncludedTerm:  7,
		KV: map[string]string{
			"a": "1",
			"b": "2",
		},
	}

	if err := p.SaveSnapshot(want); err != nil {
		t.Fatalf("SaveSnapshot() error: %v", err)
	}

	got, ok, err := p.LoadSnapshot()
	if err != nil {
		t.Fatalf("LoadSnapshot() error: %v", err)
	}
	if !ok {
		t.Fatal("LoadSnapshot() returned ok=false")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot roundtrip mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}
