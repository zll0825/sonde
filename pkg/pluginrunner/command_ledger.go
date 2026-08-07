package pluginrunner

import (
	"sync"
	"time"
)

const (
	defaultCommandLedgerCapacity = 1024
	defaultCommandLedgerTTL      = time.Hour
)

type commandExecutionResult struct {
	snapshots    []Snapshot
	ackTimestamp int64
	err          error
}

type commandLedgerEntry struct {
	done        chan struct{}
	result      commandExecutionResult
	completedAt time.Time
}

// commandLedger single-flights command execution and retains successful
// results long enough to replay lost-ACK deliveries across session reconnects.
// Failed results are released so a later Core lease can retry the provider.
type commandLedger struct {
	mu       sync.Mutex
	space    *sync.Cond
	entries  map[string]*commandLedgerEntry
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

func newCommandLedger(capacity int, ttl time.Duration) *commandLedger {
	if capacity <= 0 {
		capacity = defaultCommandLedgerCapacity
	}
	if ttl <= 0 {
		ttl = defaultCommandLedgerTTL
	}
	ledger := &commandLedger{
		entries:  make(map[string]*commandLedgerEntry),
		capacity: capacity,
		ttl:      ttl,
		now:      time.Now,
	}
	ledger.space = sync.NewCond(&ledger.mu)
	return ledger
}

func (l *commandLedger) execute(commandID string, fn func() ([]Snapshot, error)) commandExecutionResult {
	if commandID == "" {
		snapshots, err := fn()
		return commandExecutionResult{snapshots: snapshots, err: err}
	}

	l.mu.Lock()
	for {
		l.evictExpiredLocked()
		if existing, ok := l.entries[commandID]; ok {
			l.mu.Unlock()
			<-existing.done
			return cloneCommandResult(existing.result)
		}
		if len(l.entries) < l.capacity || l.evictOldestCompletedLocked() {
			break
		}
		l.space.Wait()
	}

	entry := &commandLedgerEntry{done: make(chan struct{})}
	l.entries[commandID] = entry
	l.mu.Unlock()

	snapshots, err := fn()
	completedAt := l.now()
	if err == nil {
		snapshots = cloneSnapshots(snapshots)
		for i := range snapshots {
			if snapshots[i].FetchedAt.IsZero() {
				snapshots[i].FetchedAt = completedAt
			}
		}
	}
	result := commandExecutionResult{
		snapshots:    snapshots,
		ackTimestamp: completedAt.Unix(),
		err:          err,
	}

	l.mu.Lock()
	entry.result = result
	entry.completedAt = completedAt
	close(entry.done)
	if err != nil {
		delete(l.entries, commandID)
	}
	l.space.Broadcast()
	l.mu.Unlock()
	return cloneCommandResult(result)
}

func (l *commandLedger) evictExpiredLocked() {
	cutoff := l.now().Add(-l.ttl)
	for commandID, entry := range l.entries {
		if !entry.completedAt.IsZero() && !entry.completedAt.After(cutoff) {
			delete(l.entries, commandID)
		}
	}
}

func (l *commandLedger) evictOldestCompletedLocked() bool {
	var oldestID string
	var oldestAt time.Time
	for commandID, entry := range l.entries {
		if entry.completedAt.IsZero() {
			continue
		}
		if oldestID == "" || entry.completedAt.Before(oldestAt) {
			oldestID = commandID
			oldestAt = entry.completedAt
		}
	}
	if oldestID == "" {
		return false
	}
	delete(l.entries, oldestID)
	return true
}

func cloneCommandResult(result commandExecutionResult) commandExecutionResult {
	result.snapshots = cloneSnapshots(result.snapshots)
	return result
}

func cloneSnapshots(snapshots []Snapshot) []Snapshot {
	if snapshots == nil {
		return nil
	}
	return append([]Snapshot(nil), snapshots...)
}
