package store

import "testing"

func TestMemoryStore_SetGetDelete(t *testing.T) {
	s := NewMemoryStore()

	// Set + Get
	if err := s.Set("a", "1"); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	v, err := s.Get("a")
	if err != nil || v != "1" {
		t.Fatalf("Get got (%q, %v), want (%q, nil)", v, err, "1")
	}

	// Delete
	if err := s.Delete("a"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	_, err = s.Get("a")
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}
