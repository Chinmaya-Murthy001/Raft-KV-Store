package store

import (
	"sync"
)

// MemoryStore is an in-memory key-value store.
// It is safe for concurrent use.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewMemoryStore constructs a ready-to-use MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string]string),
	}
}

func (m *MemoryStore) Set(key string, value string) error {
	if key == "" {
		return ErrKeyEmpty
	}

	m.mu.Lock()
	m.data[key] = value
	m.mu.Unlock()

	return nil
}

func (m *MemoryStore) Get(key string) (string, error) {
	if key == "" {
		return "", ErrKeyEmpty
	}

	m.mu.RLock()
	val, ok := m.data[key]
	m.mu.RUnlock()

	if !ok {
		return "", ErrKeyNotFound
	}
	return val, nil
}

func (m *MemoryStore) Delete(key string) error {
	if key == "" {
		return ErrKeyEmpty
	}

	m.mu.Lock()
	_, ok := m.data[key]
	if ok {
		delete(m.data, key)
	}
	m.mu.Unlock()

	if !ok {
		return ErrKeyNotFound
	}
	return nil
}
