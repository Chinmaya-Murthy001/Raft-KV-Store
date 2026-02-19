package raft

type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "follower"
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	default:
		return "unknown"
	}
}

const (
	OpSet    = "set"
	OpDelete = "delete"
)

type Command struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type LogEntry struct {
	Term    int     `json:"term"`
	Index   int     `json:"index"`
	Command Command `json:"command"`
}
