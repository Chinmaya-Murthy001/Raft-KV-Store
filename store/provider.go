package store

// NewDefaultStore centralizes the concrete store selection.
// Swap this to Raft/replicated store later without changing API wiring.
func NewDefaultStore() Store {
	return NewMemoryStore()
}
