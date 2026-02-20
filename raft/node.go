package raft

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"kvstore/store"
)

type Node struct {
	mu sync.RWMutex

	id       string
	raftPort string
	peers    []string

	state State

	// Persistent-ish state (we will persist these later).
	currentTerm  int
	votedFor     string
	logBaseIndex int
	logBaseTerm  int
	log          []LogEntry

	// Volatile state on all servers.
	commitIndex int
	lastApplied int

	// Volatile state on leaders.
	nextIndex  map[string]int
	matchIndex map[string]int

	leaderID      string
	leaderAddr    string
	lastHeartbeat time.Time

	votesReceived   int
	electionTerm    int
	electionTimeout time.Duration
	electionDueAt   time.Time

	transport *Transport
	rng       *rand.Rand
	server    *http.Server
	loopStop  chan struct{}
	loopDone  chan struct{}
	applyDone chan struct{}
	logger    *log.Logger
	persister *Persister

	kv                store.Store
	applyCh           chan struct{}
	applyLoopRunning  bool
	snapshotThreshold int
	pendingSnapshot   *Snapshot
	waitCh            map[int]chan struct{}
	clientAddr        string
	leaderClientByID  map[string]string
	leaderRaftByID    map[string]string
}

type NotLeaderError struct {
	LeaderID string
	Leader   string
}

func (e *NotLeaderError) Error() string {
	if e == nil || e.Leader == "" {
		return "not leader"
	}
	return fmt.Sprintf("not leader; leader=%s", e.Leader)
}

var ErrProposalTimeout = errors.New("proposal timeout")

type snapshotStore interface {
	Snapshot() map[string]string
	Restore(map[string]string)
}

func NewNode(id string, raftPort string, peers []string, logger *log.Logger) *Node {
	if strings.TrimSpace(id) == "" {
		id = "node"
	}
	if strings.TrimSpace(raftPort) == "" {
		raftPort = "9080"
	}

	n := &Node{
		id:                id,
		raftPort:          raftPort,
		peers:             append([]string{}, peers...),
		state:             Follower,
		nextIndex:         make(map[string]int),
		matchIndex:        make(map[string]int),
		transport:         NewTransport(),
		rng:               rand.New(rand.NewSource(time.Now().UnixNano() + int64(stableIDHash(id)))),
		logger:            logger,
		applyCh:           make(chan struct{}, 1),
		snapshotThreshold: 50,
		waitCh:            make(map[int]chan struct{}),
		leaderClientByID:  make(map[string]string),
		leaderRaftByID:    make(map[string]string),
	}

	return n
}

func (n *Node) raftAddr() string {
	if strings.HasPrefix(n.raftPort, ":") {
		return n.raftPort
	}
	return ":" + n.raftPort
}

func (n *Node) selfRaftURL() string {
	return "http://localhost" + n.raftAddr()
}

func (n *Node) selfClientURLLocked() string {
	if n.clientAddr != "" {
		return n.clientAddr
	}
	return n.selfRaftURL()
}

// Start launches the Raft RPC HTTP server and background election/timer loops.
func (n *Node) Start() error {
	n.mu.Lock()
	if n.server != nil {
		n.mu.Unlock()
		return errors.New("raft server already started")
	}
	n.resetElectionTimerLocked()

	mux := http.NewServeMux()
	n.registerHTTPRoutes(mux)

	n.server = &http.Server{
		Addr:    n.raftAddr(),
		Handler: mux,
	}
	n.loopStop = make(chan struct{})
	n.loopDone = make(chan struct{})
	n.applyDone = make(chan struct{})
	n.applyLoopRunning = true
	go n.runLoop(n.loopStop, n.loopDone)
	go n.applyLoop(n.loopStop, n.applyDone)

	addr := n.server.Addr
	id := n.id
	logger := n.logger
	n.mu.Unlock()

	if logger != nil {
		logger.Printf("[raft] %s listening on http://localhost%s", id, addr)
	}

	err := n.server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (n *Node) Stop(ctx context.Context) error {
	n.mu.Lock()
	srv := n.server
	stopCh := n.loopStop
	doneCh := n.loopDone
	applyDone := n.applyDone
	n.server = nil
	n.loopStop = nil
	n.loopDone = nil
	n.applyDone = nil
	n.applyLoopRunning = false
	n.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
	if applyDone != nil {
		<-applyDone
	}

	if srv == nil {
		return nil
	}
	if ctx == nil {
		localCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ctx = localCtx
	}
	return srv.Shutdown(ctx)
}

func (n *Node) runLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			n.mu.Lock()
			if n.state != Leader && time.Now().After(n.electionDueAt) {
				term, cid, lastLogIndex, lastLogTerm, peers, majority := n.startElectionLocked()
				if n.votesReceived >= majority {
					n.becomeLeaderLocked()
					n.mu.Unlock()
					go n.heartbeatLoop(stop, term)
					continue
				}
				n.mu.Unlock()
				n.requestVotes(stop, term, cid, lastLogIndex, lastLogTerm, peers, majority)
				continue
			}

			n.mu.Unlock()
		}
	}
}

func (n *Node) heartbeatLoop(stop <-chan struct{}, leaderTerm int) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			n.mu.RLock()
			activeLeader := n.state == Leader && n.currentTerm == leaderTerm
			n.mu.RUnlock()
			if !activeLeader {
				return
			}
			n.replicateOnce(leaderTerm)
		}
	}
}

func (n *Node) requestVotes(stop <-chan struct{}, term int, candidateID string, lastLogIndex int, lastLogTerm int, peers []string, majority int) {
	req := RequestVoteRequest{
		Term:         term,
		CandidateID:  candidateID,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	for _, p := range peers {
		peer := p
		go func() {
			select {
			case <-stop:
				return
			default:
			}

			resp, err := n.transport.SendRequestVote(peer, req)
			if err != nil {
				return
			}

			n.mu.Lock()
			defer n.mu.Unlock()

			if resp.Term > n.currentTerm {
				dirty := n.becomeFollowerLocked(resp.Term, "")
				if dirty {
					n.persistLocked()
				}
				if n.logger != nil {
					n.logger.Printf("[raft] %s stepped down due to higher term=%d during vote", n.id, resp.Term)
				}
				return
			}

			if n.state != Candidate || n.currentTerm != term || n.electionTerm != term {
				return
			}

			if !resp.VoteGranted {
				return
			}

			n.votesReceived++
			if n.logger != nil {
				n.logger.Printf("[raft] %s got vote from %s (%d/%d) term=%d", n.id, peer, n.votesReceived, majority, n.currentTerm)
			}

			if n.votesReceived >= majority {
				n.becomeLeaderLocked()
				go n.heartbeatLoop(stop, n.currentTerm)
			}
		}()
	}
}

func (n *Node) resetElectionTimerLocked() {
	// 600-1200ms keeps local Windows demos stable.
	ms := 600 + n.rng.Intn(601)
	n.electionTimeout = time.Duration(ms) * time.Millisecond
	n.electionDueAt = time.Now().Add(n.electionTimeout)
}

func stableIDHash(s string) int {
	h := 0
	for i := 0; i < len(s); i++ {
		h = h*131 + int(s[i])
	}
	if h < 0 {
		return -h
	}
	return h
}

func (n *Node) startElectionLocked() (term int, candidateID string, lastLogIndex int, lastLogTerm int, peers []string, majority int) {
	n.state = Candidate
	n.currentTerm++
	n.electionTerm = n.currentTerm
	n.votedFor = n.id
	n.votesReceived = 1 // self vote
	n.leaderID = ""
	n.resetElectionTimerLocked()
	n.persistLocked()

	total := len(n.peers) + 1
	majority = total/2 + 1

	if n.logger != nil {
		n.logger.Printf("[raft] %s became candidate term=%d (%d/%d)", n.id, n.currentTerm, n.votesReceived, majority)
	}

	return n.currentTerm, n.id, n.lastLogIndexLocked(), n.lastLogTermLocked(), append([]string{}, n.peers...), majority
}

func (n *Node) becomeLeaderLocked() {
	n.state = Leader
	n.leaderID = n.id
	n.leaderAddr = n.selfClientURLLocked()
	n.leaderClientByID[n.id] = n.leaderAddr
	n.leaderRaftByID[n.id] = n.selfRaftURL()
	votedForChanged := n.votedFor != ""
	n.votedFor = ""
	n.votesReceived = 0
	n.electionTerm = 0

	next := n.lastLogIndexLocked() + 1
	for _, peer := range n.peers {
		n.nextIndex[peer] = next
		n.matchIndex[peer] = 0
	}

	if votedForChanged {
		n.persistLocked()
	}

	if n.logger != nil {
		n.logger.Printf("[raft] %s became leader term=%d", n.id, n.currentTerm)
	}
}

func (n *Node) becomeFollowerLocked(term int, leaderID string) bool {
	stableDirty := false
	if term > n.currentTerm {
		n.currentTerm = term
		stableDirty = true
	}
	n.state = Follower
	if n.votedFor != "" {
		stableDirty = true
	}
	n.votedFor = ""
	n.votesReceived = 0
	n.electionTerm = 0
	n.leaderID = leaderID
	if leaderID == "" {
		n.leaderAddr = ""
	} else if addr := n.leaderClientByID[leaderID]; addr != "" {
		n.leaderAddr = addr
	}
	n.resetElectionTimerLocked()
	return stableDirty
}

func (n *Node) lastLogIndexLocked() int {
	if len(n.log) == 0 {
		return n.logBaseIndex
	}
	return n.log[len(n.log)-1].Index
}

func (n *Node) lastLogTermLocked() int {
	if len(n.log) == 0 {
		return n.logBaseTerm
	}
	return n.log[len(n.log)-1].Term
}

func (n *Node) candidateLogUpToDateLocked(lastLogIndex int, lastLogTerm int) bool {
	myLastTerm := n.lastLogTermLocked()
	if lastLogTerm != myLastTerm {
		return lastLogTerm > myLastTerm
	}
	return lastLogIndex >= n.lastLogIndexLocked()
}

func (n *Node) buildAppendEntriesForPeerLocked(peer string) (AppendEntriesRequest, int) {
	lastLogIndex := n.lastLogIndexLocked()
	nextIdx, ok := n.nextIndex[peer]
	minNext := n.logBaseIndex + 1
	if !ok || nextIdx < minNext {
		nextIdx = minNext
	}
	if nextIdx > lastLogIndex+1 {
		nextIdx = lastLogIndex + 1
	}
	n.nextIndex[peer] = nextIdx

	prevLogIndex := nextIdx - 1
	prevLogTerm, _ := n.logTermAtIndexLocked(prevLogIndex)

	entries := make([]LogEntry, 0)
	if nextIdx <= lastLogIndex {
		startPos := n.indexToSlicePos(nextIdx)
		if startPos < 0 {
			startPos = 0
		}
		entries = append(entries, n.log[startPos:]...)
	}

	return AppendEntriesRequest{
		Term:         n.currentTerm,
		LeaderID:     n.id,
		LeaderAddr:   n.selfClientURLLocked(),
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: n.commitIndex,
	}, len(entries)
}

func (n *Node) handleAppendResponseLocked(peer string, requestTerm int, prevLogIndex int, sentLen int, resp AppendEntriesResponse) {
	if resp.Term > n.currentTerm {
		dirty := n.becomeFollowerLocked(resp.Term, "")
		if dirty {
			n.persistLocked()
		}
		if n.logger != nil {
			n.logger.Printf("[raft] %s stepped down due to higher term=%d from %s", n.id, resp.Term, peer)
		}
		return
	}

	if n.state != Leader || n.currentTerm != requestTerm {
		return
	}

	if resp.Success {
		replicatedUpTo := prevLogIndex + sentLen
		if replicatedUpTo > n.matchIndex[peer] {
			n.matchIndex[peer] = replicatedUpTo
		}

		next := n.matchIndex[peer] + 1
		minNext := n.logBaseIndex + 1
		maxNext := n.lastLogIndexLocked() + 1
		if next < minNext {
			next = minNext
		}
		if next > maxNext {
			next = maxNext
		}
		n.nextIndex[peer] = next
		n.advanceCommitIndexLocked()
		return
	}

	next := n.nextIndex[peer]
	minNext := n.logBaseIndex + 1
	if next > minNext {
		next--
	} else {
		next = minNext
	}
	n.nextIndex[peer] = next

	if n.logger != nil {
		n.logger.Printf("[raft] %s append rejected by %s; backing off nextIndex=%d", n.id, peer, next)
	}
}

func (n *Node) replicateOnce(leaderTerm int) {
	n.mu.RLock()
	if n.state != Leader || n.currentTerm != leaderTerm {
		n.mu.RUnlock()
		return
	}
	peers := append([]string{}, n.peers...)
	n.mu.RUnlock()

	for _, p := range peers {
		peer := p

		n.mu.Lock()
		if n.state != Leader || n.currentTerm != leaderTerm {
			n.mu.Unlock()
			return
		}
		req, sentLen := n.buildAppendEntriesForPeerLocked(peer)
		n.mu.Unlock()

		go func(request AppendEntriesRequest, sentCount int) {
			resp, err := n.transport.SendAppendEntries(peer, request)
			if err != nil {
				return
			}

			n.mu.Lock()
			n.handleAppendResponseLocked(peer, request.Term, request.PrevLogIndex, sentCount, resp)
			n.mu.Unlock()
		}(req, sentLen)
	}
}

func (n *Node) majority() int {
	total := len(n.peers) + 1
	return total/2 + 1
}

func (n *Node) logTermAtIndexLocked(index int) (int, bool) {
	if index < 0 {
		return 0, false
	}
	if index == 0 {
		return 0, true
	}
	if index == n.logBaseIndex {
		return n.logBaseTerm, true
	}
	if index < n.logBaseIndex {
		return 0, false
	}

	pos := n.indexToSlicePos(index)
	if pos < 0 || pos >= len(n.log) {
		return 0, false
	}

	entry := n.log[pos]
	if entry.Index == index {
		return entry.Term, true
	}

	// Fallback in case index fields are not perfectly contiguous.
	for i := len(n.log) - 1; i >= 0; i-- {
		if n.log[i].Index == index {
			return n.log[i].Term, true
		}
	}

	return 0, false
}

func (n *Node) advanceCommitIndexLocked() {
	if n.state != Leader {
		return
	}

	last := n.lastLogIndexLocked()
	majority := n.majority()

	for idx := last; idx > n.commitIndex; idx-- {
		term, ok := n.logTermAtIndexLocked(idx)
		if !ok || term != n.currentTerm {
			continue
		}

		count := 1 // leader itself
		for _, peer := range n.peers {
			if n.matchIndex[peer] >= idx {
				count++
			}
		}

		if count >= majority {
			n.commitIndex = idx
			n.notifyCommitWaitersLocked()
			n.signalApplyLocked()
			return
		}
	}
}

func (n *Node) notifyCommitWaitersLocked() {
	for idx, ch := range n.waitCh {
		if idx <= n.commitIndex {
			close(ch)
			delete(n.waitCh, idx)
		}
	}
}

func (n *Node) logEntryAtIndexLocked(index int) (LogEntry, bool) {
	if index <= n.logBaseIndex {
		return LogEntry{}, false
	}

	pos := n.indexToSlicePos(index)
	if pos >= 0 && pos < len(n.log) && n.log[pos].Index == index {
		return n.log[pos], true
	}

	// Fallback if index metadata is sparse/non-contiguous.
	for i := range n.log {
		if n.log[i].Index == index {
			return n.log[i], true
		}
	}

	return LogEntry{}, false
}

func (n *Node) signalApplyLocked() {
	select {
	case n.applyCh <- struct{}{}:
	default:
	}
}

func (n *Node) signalApply() {
	n.mu.RLock()
	running := n.applyLoopRunning
	n.mu.RUnlock()
	if !running {
		n.applyCommittedEntries()
		return
	}

	select {
	case n.applyCh <- struct{}{}:
	default:
	}
}

func (n *Node) applyLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			n.applyCommittedEntries()
		case <-n.applyCh:
			n.applyCommittedEntries()
		}
	}
}

func (n *Node) applyCommittedEntries() {
	for {
		n.mu.Lock()
		if n.lastApplied >= n.commitIndex {
			n.mu.Unlock()
			return
		}

		nextToApply := n.lastApplied + 1
		entry, ok := n.logEntryAtIndexLocked(nextToApply)
		n.mu.Unlock()
		if !ok {
			return
		}

		if err := n.applyEntry(entry); err != nil {
			if n.logger != nil {
				n.logger.Printf("[raft] apply error at index=%d term=%d err=%v", entry.Index, entry.Term, err)
			}
			return
		}

		n.mu.Lock()
		if n.lastApplied+1 == entry.Index {
			n.lastApplied = entry.Index
			n.maybeSnapshotLocked()
		}
		n.mu.Unlock()
	}
}

func (n *Node) maybeSnapshotLocked() {
	if n.persister == nil || n.snapshotThreshold <= 0 {
		return
	}
	if n.lastApplied <= n.logBaseIndex {
		return
	}
	if n.lastApplied-n.logBaseIndex < n.snapshotThreshold {
		return
	}

	s, ok := n.kv.(snapshotStore)
	if !ok {
		return
	}

	snapIndex := n.lastApplied
	snapTerm, ok := n.logTermAtIndexLocked(snapIndex)
	if !ok {
		return
	}

	snap := Snapshot{
		LastIncludedIndex: snapIndex,
		LastIncludedTerm:  snapTerm,
		KV:                s.Snapshot(),
	}
	if err := n.persister.SaveSnapshot(snap); err != nil {
		if n.logger != nil {
			n.logger.Printf("[raft] snapshot save failed: %v", err)
		}
		return
	}

	startPos := n.indexToSlicePos(snapIndex + 1)
	if startPos < 0 {
		startPos = 0
	}
	if startPos > len(n.log) {
		startPos = len(n.log)
	}
	if startPos == len(n.log) {
		n.log = n.log[:0]
	} else {
		n.log = append([]LogEntry(nil), n.log[startPos:]...)
	}
	n.logBaseIndex = snapIndex
	n.logBaseTerm = snapTerm
	n.persistLocked()

	if n.logger != nil {
		n.logger.Printf("[raft] snapshot created index=%d term=%d logLen=%d", snapIndex, snapTerm, len(n.log))
	}
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (n *Node) applyEntry(entry LogEntry) error {
	if n.kv == nil {
		return nil
	}

	if n.logger != nil {
		n.logger.Printf("[apply] index=%d op=%s key=%s value=%s", entry.Index, entry.Command.Op, entry.Command.Key, entry.Command.Value)
	}

	switch entry.Command.Op {
	case OpSet:
		return n.kv.Set(entry.Command.Key, entry.Command.Value)
	case OpDelete:
		return n.kv.Delete(entry.Command.Key)
	default:
		return fmt.Errorf("unknown op: %s", entry.Command.Op)
	}
}

func (n *Node) BindStore(kv store.Store) {
	n.mu.Lock()
	n.kv = kv
	if n.pendingSnapshot != nil {
		if s, ok := n.kv.(snapshotStore); ok {
			s.Restore(copyStringMap(n.pendingSnapshot.KV))
			n.pendingSnapshot = nil
		}
	}
	n.mu.Unlock()
}

func (n *Node) SetSnapshotThreshold(threshold int) {
	n.mu.Lock()
	if threshold > 0 {
		n.snapshotThreshold = threshold
	}
	n.mu.Unlock()
}

func (n *Node) BindClientAddr(addr string) {
	n.mu.Lock()
	n.clientAddr = strings.TrimSpace(addr)
	if n.clientAddr != "" && n.id != "" {
		n.leaderClientByID[n.id] = n.clientAddr
	}
	n.mu.Unlock()
}

func (n *Node) BindAddressBook(nodeKV map[string]string, nodeRaft map[string]string) {
	n.mu.Lock()
	for id, kv := range nodeKV {
		trimmed := strings.TrimSpace(kv)
		if id != "" && trimmed != "" {
			n.leaderClientByID[id] = trimmed
		}
	}
	for id, raftAddr := range nodeRaft {
		trimmed := strings.TrimSpace(raftAddr)
		if id != "" && trimmed != "" {
			n.leaderRaftByID[id] = trimmed
		}
	}
	n.mu.Unlock()
}

func (n *Node) InitPersistence(dataDir string) error {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return errors.New("raft data dir is empty")
	}

	p := NewPersister(dataDir)
	snap, hasSnapshot, err := p.LoadSnapshot()
	if err != nil {
		return err
	}
	st, ok, err := p.Load()
	if err != nil {
		return err
	}

	n.mu.Lock()
	n.persister = p
	n.state = Follower
	n.commitIndex = 0
	n.lastApplied = 0
	n.logBaseIndex = 0
	n.logBaseTerm = 0
	n.nextIndex = make(map[string]int)
	n.matchIndex = make(map[string]int)
	n.pendingSnapshot = nil
	if hasSnapshot {
		n.logBaseIndex = snap.LastIncludedIndex
		n.logBaseTerm = snap.LastIncludedTerm
		n.commitIndex = n.logBaseIndex
		n.lastApplied = n.logBaseIndex
		if n.kv != nil {
			if s, ok := n.kv.(snapshotStore); ok {
				s.Restore(copyStringMap(snap.KV))
			} else {
				n.pendingSnapshot = &Snapshot{
					LastIncludedIndex: snap.LastIncludedIndex,
					LastIncludedTerm:  snap.LastIncludedTerm,
					KV:                copyStringMap(snap.KV),
				}
			}
		} else {
			n.pendingSnapshot = &Snapshot{
				LastIncludedIndex: snap.LastIncludedIndex,
				LastIncludedTerm:  snap.LastIncludedTerm,
				KV:                copyStringMap(snap.KV),
			}
		}
	}
	if ok {
		n.currentTerm = st.CurrentTerm
		n.votedFor = st.VotedFor
		n.log = append([]LogEntry(nil), st.Log...)
		if st.LogBaseIndex > n.logBaseIndex {
			n.logBaseIndex = st.LogBaseIndex
			n.logBaseTerm = st.LogBaseTerm
		}
	} else {
		n.currentTerm = 0
		n.votedFor = ""
		n.log = make([]LogEntry, 0)
	}
	if n.commitIndex < n.logBaseIndex {
		n.commitIndex = n.logBaseIndex
	}
	if n.lastApplied < n.logBaseIndex {
		n.lastApplied = n.logBaseIndex
	}
	n.persistLocked()
	n.mu.Unlock()

	return nil
}

func (n *Node) stableStateLocked() StableState {
	return StableState{
		CurrentTerm:  n.currentTerm,
		VotedFor:     n.votedFor,
		LogBaseIndex: n.logBaseIndex,
		LogBaseTerm:  n.logBaseTerm,
		Log:          append([]LogEntry(nil), n.log...),
	}
}

func (n *Node) persistLocked() {
	if n.persister == nil {
		return
	}
	if err := n.persister.Save(n.stableStateLocked()); err != nil && n.logger != nil {
		n.logger.Printf("[raft] persist failed: %v", err)
	}
}

func (n *Node) Propose(cmd Command) (index int, term int, err error) {
	if strings.TrimSpace(cmd.Key) == "" {
		return 0, 0, store.ErrKeyEmpty
	}
	if cmd.Op != OpSet && cmd.Op != OpDelete {
		return 0, 0, fmt.Errorf("unknown op: %s", cmd.Op)
	}

	n.mu.Lock()
	if n.state != Leader {
		leaderID := n.leaderID
		leader := ""
		if leaderID != "" {
			leader = n.leaderClientByID[leaderID]
		}
		if leader == "" {
			leader = n.leaderAddr
		}
		n.mu.Unlock()
		return 0, 0, &NotLeaderError{LeaderID: leaderID, Leader: leader}
	}

	term = n.currentTerm
	index = n.lastLogIndexLocked() + 1
	entry := LogEntry{
		Term:    term,
		Index:   index,
		Command: cmd,
	}
	n.log = append(n.log, entry)
	n.persistLocked()

	wait := make(chan struct{})
	n.waitCh[index] = wait

	n.advanceCommitIndexLocked()
	n.mu.Unlock()

	n.signalApply()
	n.replicateOnce(term)

	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	select {
	case <-wait:
		return index, term, nil
	case <-timeout.C:
		n.mu.Lock()
		delete(n.waitCh, index)
		n.mu.Unlock()
		return index, term, ErrProposalTimeout
	}
}

func (n *Node) indexToSlicePos(index int) int {
	// Log contains entries for indices > logBaseIndex.
	return index - n.logBaseIndex - 1
}

type Status struct {
	NodeID       string         `json:"node_id"`
	State        string         `json:"state"`
	Term         int            `json:"term"`
	CommitIndex  int            `json:"commitIndex"`
	LastApplied  int            `json:"lastApplied"`
	LogLen       int            `json:"logLen"`
	LastLogIndex int            `json:"lastLogIndex"`
	LastLogTerm  int            `json:"lastLogTerm"`
	LogBaseIndex int            `json:"logBaseIndex"`
	LogBaseTerm  int            `json:"logBaseTerm"`
	Peers        []string       `json:"peers"`
	NextIndex    map[string]int `json:"nextIndex,omitempty"`
	MatchIndex   map[string]int `json:"matchIndex,omitempty"`

	// Compatibility fields used by existing /health endpoint.
	ID                 string `json:"id,omitempty"`
	LeaderID           string `json:"leaderId,omitempty"`
	LeaderAddr         string `json:"leaderAddr,omitempty"`
	PeersCount         int    `json:"peersCount"`
	LastHeartbeatAgoMs int64  `json:"lastHeartbeatAgoMs"`
}

// Status returns a JSON-ready state snapshot.
func (n *Node) Status() Status {
	n.mu.RLock()
	defer n.mu.RUnlock()

	lastHeartbeatAgo := int64(-1)
	if !n.lastHeartbeat.IsZero() {
		lastHeartbeatAgo = time.Since(n.lastHeartbeat).Milliseconds()
	}

	return Status{
		NodeID:             n.id,
		State:              n.state.String(),
		Term:               n.currentTerm,
		CommitIndex:        n.commitIndex,
		LastApplied:        n.lastApplied,
		LogLen:             len(n.log),
		LastLogIndex:       n.lastLogIndexLocked(),
		LastLogTerm:        n.lastLogTermLocked(),
		LogBaseIndex:       n.logBaseIndex,
		LogBaseTerm:        n.logBaseTerm,
		Peers:              append([]string{}, n.peers...),
		ID:                 n.id,
		LeaderID:           n.leaderID,
		LeaderAddr:         n.leaderAddr,
		PeersCount:         len(n.peers),
		LastHeartbeatAgoMs: lastHeartbeatAgo,
		NextIndex:          statusNextIndex(n.state, n.nextIndex),
		MatchIndex:         statusNextIndex(n.state, n.matchIndex),
	}
}

func statusNextIndex(state State, src map[string]int) map[string]int {
	if state != Leader || len(src) == 0 {
		return nil
	}
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (n *Node) HandleRequestVote(req RequestVoteRequest) RequestVoteResponse {
	n.mu.Lock()
	defer n.mu.Unlock()
	dirty := false

	if req.Term < n.currentTerm {
		return RequestVoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	if req.Term > n.currentTerm {
		if n.becomeFollowerLocked(req.Term, "") {
			dirty = true
		}
	}

	if !n.candidateLogUpToDateLocked(req.LastLogIndex, req.LastLogTerm) {
		if dirty {
			n.persistLocked()
		}
		return RequestVoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	if n.votedFor == "" || n.votedFor == req.CandidateID {
		if n.votedFor != req.CandidateID {
			dirty = true
		}
		n.votedFor = req.CandidateID
		n.resetElectionTimerLocked()
		if dirty {
			n.persistLocked()
		}
		return RequestVoteResponse{Term: n.currentTerm, VoteGranted: true}
	}

	if dirty {
		n.persistLocked()
	}
	return RequestVoteResponse{Term: n.currentTerm, VoteGranted: false}
}

func (n *Node) HandleAppendEntries(req AppendEntriesRequest) AppendEntriesResponse {
	n.mu.Lock()
	dirty := false
	commitAdvanced := false

	if req.Term < n.currentTerm {
		resp := AppendEntriesResponse{Term: n.currentTerm, Success: false}
		n.mu.Unlock()
		return resp
	}

	if req.Term > n.currentTerm {
		if n.becomeFollowerLocked(req.Term, req.LeaderID) {
			dirty = true
		}
	} else if req.Term == n.currentTerm {
		if n.state != Follower || (req.LeaderID != "" && req.LeaderID != n.id) {
			n.state = Follower
			if n.votedFor != "" {
				dirty = true
			}
			n.votedFor = ""
			n.votesReceived = 0
			n.electionTerm = 0
		}
		n.leaderID = req.LeaderID
		n.leaderAddr = req.LeaderAddr
		if req.LeaderID != "" && req.LeaderAddr != "" {
			n.leaderClientByID[req.LeaderID] = req.LeaderAddr
		}
		n.resetElectionTimerLocked()
	}

	if req.LeaderAddr != "" {
		n.leaderAddr = req.LeaderAddr
	}
	if req.LeaderID != "" && req.LeaderAddr != "" {
		n.leaderClientByID[req.LeaderID] = req.LeaderAddr
	}

	n.lastHeartbeat = time.Now()

	if req.PrevLogIndex > n.lastLogIndexLocked() {
		if dirty {
			n.persistLocked()
		}
		resp := AppendEntriesResponse{Term: n.currentTerm, Success: false}
		n.mu.Unlock()
		return resp
	}

	if req.PrevLogIndex < n.logBaseIndex {
		if dirty {
			n.persistLocked()
		}
		resp := AppendEntriesResponse{Term: n.currentTerm, Success: false}
		n.mu.Unlock()
		return resp
	}

	if req.PrevLogIndex > 0 {
		prevTerm, okPrev := n.logTermAtIndexLocked(req.PrevLogIndex)
		if !okPrev || prevTerm != req.PrevLogTerm {
			if dirty {
				n.persistLocked()
			}
			resp := AppendEntriesResponse{Term: n.currentTerm, Success: false}
			n.mu.Unlock()
			return resp
		}
	}

	for _, entry := range req.Entries {
		if entry.Index <= n.logBaseIndex {
			if entry.Index == n.logBaseIndex && entry.Term != n.logBaseTerm {
				if dirty {
					n.persistLocked()
				}
				resp := AppendEntriesResponse{Term: n.currentTerm, Success: false}
				n.mu.Unlock()
				return resp
			}
			continue
		}

		pos := n.indexToSlicePos(entry.Index)
		if pos < 0 {
			if dirty {
				n.persistLocked()
			}
			resp := AppendEntriesResponse{Term: n.currentTerm, Success: false}
			n.mu.Unlock()
			return resp
		}

		switch {
		case pos < len(n.log):
			if n.log[pos].Term != entry.Term {
				n.log = n.log[:pos]
				n.log = append(n.log, entry)
				dirty = true
			}
		case pos == len(n.log):
			n.log = append(n.log, entry)
			dirty = true
		default:
			if dirty {
				n.persistLocked()
			}
			resp := AppendEntriesResponse{Term: n.currentTerm, Success: false}
			n.mu.Unlock()
			return resp
		}
	}

	if req.LeaderCommit > n.commitIndex {
		last := n.lastLogIndexLocked()
		prevCommit := n.commitIndex
		if req.LeaderCommit < last {
			n.commitIndex = req.LeaderCommit
		} else {
			n.commitIndex = last
		}
		commitAdvanced = n.commitIndex > prevCommit
	}

	if dirty {
		n.persistLocked()
	}
	if commitAdvanced {
		n.signalApplyLocked()
	}
	resp := AppendEntriesResponse{Term: n.currentTerm, Success: true}
	n.mu.Unlock()
	return resp
}
