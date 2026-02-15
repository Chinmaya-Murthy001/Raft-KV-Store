package store

import (
	"fmt"
	"testing"
)

func BenchmarkMemoryStore_Set(b *testing.B) {
	s := NewMemoryStore()
	for i := 0; i < b.N; i++ {
		_ = s.Set(fmt.Sprintf("k-%d", i), "value")
	}
}

func BenchmarkMemoryStore_Get(b *testing.B) {
	s := NewMemoryStore()
	for i := 0; i < 100000; i++ {
		_ = s.Set(fmt.Sprintf("k-%d", i), "value")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Get("k-99999")
	}
}
