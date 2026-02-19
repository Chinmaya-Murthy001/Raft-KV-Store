package raft

import (
	"testing"

	"kvstore/store"
)

type fakeKV struct {
	data map[string]string
}

func newFakeKV() *fakeKV {
	return &fakeKV{data: make(map[string]string)}
}

func (f *fakeKV) Set(key string, value string) error {
	if key == "" {
		return store.ErrKeyEmpty
	}
	f.data[key] = value
	return nil
}

func (f *fakeKV) Get(key string) (string, error) {
	if key == "" {
		return "", store.ErrKeyEmpty
	}
	v, ok := f.data[key]
	if !ok {
		return "", store.ErrKeyNotFound
	}
	return v, nil
}

func (f *fakeKV) Delete(key string) error {
	if key == "" {
		return store.ErrKeyEmpty
	}
	if _, ok := f.data[key]; !ok {
		return store.ErrKeyNotFound
	}
	delete(f.data, key)
	return nil
}

func TestApplyCommittedOnly(t *testing.T) {
	kv := newFakeKV()
	n := NewNode("n1", "9931", nil, nil)
	n.BindStore(kv)

	n.mu.Lock()
	n.log = []LogEntry{
		{Index: 1, Term: 1, Command: Command{Op: OpSet, Key: "a", Value: "1"}},
		{Index: 2, Term: 1, Command: Command{Op: OpSet, Key: "b", Value: "2"}},
	}
	n.commitIndex = 1
	n.applyCommittedLocked()
	firstBatch := n.drainApplyQueueLocked()
	n.mu.Unlock()
	n.dispatchApply(firstBatch)

	if v, err := kv.Get("a"); err != nil || v != "1" {
		t.Fatalf("expected key a to be applied first: got v=%q err=%v", v, err)
	}
	if _, err := kv.Get("b"); err != store.ErrKeyNotFound {
		t.Fatalf("expected key b to remain unapplied before commit, err=%v", err)
	}

	n.mu.Lock()
	n.commitIndex = 2
	n.applyCommittedLocked()
	secondBatch := n.drainApplyQueueLocked()
	n.mu.Unlock()
	n.dispatchApply(secondBatch)

	if v, err := kv.Get("b"); err != nil || v != "2" {
		t.Fatalf("expected key b after commit: got v=%q err=%v", v, err)
	}

	n.mu.RLock()
	if n.lastApplied != 2 {
		n.mu.RUnlock()
		t.Fatalf("lastApplied=%d want 2", n.lastApplied)
	}
	n.mu.RUnlock()
}
