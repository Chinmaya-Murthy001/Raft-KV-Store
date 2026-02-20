package raft

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type StableState struct {
	CurrentTerm  int        `json:"current_term"`
	VotedFor     string     `json:"voted_for"`
	LogBaseIndex int        `json:"log_base_index"`
	LogBaseTerm  int        `json:"log_base_term"`
	Log          []LogEntry `json:"log"`
}

type Snapshot struct {
	LastIncludedIndex int               `json:"last_included_index"`
	LastIncludedTerm  int               `json:"last_included_term"`
	KV                map[string]string `json:"kv"`
}

type Persister struct {
	dir              string
	filename         string
	snapshotFilename string
}

func NewPersister(dataDir string) *Persister {
	return &Persister{
		dir:              dataDir,
		filename:         filepath.Join(dataDir, "raft_state.json"),
		snapshotFilename: filepath.Join(dataDir, "snapshot.json"),
	}
}

func (p *Persister) Load() (StableState, bool, error) {
	var st StableState

	b, err := os.ReadFile(p.filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, false, nil
		}
		return st, false, fmt.Errorf("read stable state: %w", err)
	}

	if err := json.Unmarshal(b, &st); err != nil {
		return st, false, fmt.Errorf("unmarshal stable state: %w", err)
	}
	return st, true, nil
}

func (p *Persister) Save(st StableState) error {
	return p.saveJSONAtomic(p.filename, st, "stable state")
}

func (p *Persister) LoadSnapshot() (Snapshot, bool, error) {
	var snap Snapshot

	b, err := os.ReadFile(p.snapshotFilename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snap, false, nil
		}
		return snap, false, fmt.Errorf("read snapshot: %w", err)
	}

	if err := json.Unmarshal(b, &snap); err != nil {
		return snap, false, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return snap, true, nil
}

func (p *Persister) SaveSnapshot(snap Snapshot) error {
	return p.saveJSONAtomic(p.snapshotFilename, snap, "snapshot")
}

func (p *Persister) saveJSONAtomic(filename string, payload any, label string) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", label, err)
	}

	tmp := filename + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open tmp %s: %w", label, err)
	}

	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("write tmp %s: %w", label, err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync tmp %s: %w", label, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close tmp %s: %w", label, err)
	}

	if err := os.Rename(tmp, filename); err != nil {
		return fmt.Errorf("rename tmp %s: %w", label, err)
	}

	if dirF, err := os.Open(p.dir); err == nil {
		_ = dirF.Sync()
		_ = dirF.Close()
	}

	return nil
}
