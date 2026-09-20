// Package queue implements a deterministic, concurrent, auditable
// priority-assignment engine.
//
// The package knows nothing about patients, doctors, or any other
// domain concept. It accepts commands (AddEntry, UpdatePriority,
// RemoveEntry, AssignNext, CancelAssignment) and returns the resulting
// immutable events. Ordering follows two rules: lower Priority (rank)
// first, then earlier Sequence first. Persistence, transport, and
// domain meaning all belong to the host application.
package queue

import (
	"container/heap"
	"sync"
	"time"
)

// heapItem wraps an Entry with the bookkeeping container/heap needs to
// support in-place priority updates. It is never exposed outside the
// package.
type heapItem struct {
	entry *Entry
	pos   int // current position of this item within entryHeap
}

// entryHeap orders waiting entries by priority, then by sequence — the
// engine's entire v1 ordering policy.
type entryHeap []*heapItem

func (h entryHeap) Len() int { return len(h) }

func (h entryHeap) Less(i, j int) bool {
	if h[i].entry.Priority != h[j].entry.Priority {
		return h[i].entry.Priority < h[j].entry.Priority
	}
	return h[i].entry.Sequence < h[j].entry.Sequence
}

func (h entryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].pos = i
	h[j].pos = j
}

func (h *entryHeap) Push(x any) {
	item := x.(*heapItem)
	item.pos = len(*h)
	*h = append(*h, item)
}

func (h *entryHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.pos = -1
	*h = old[:n-1]
	return item
}

// Queue is a deterministic, concurrency-safe priority-assignment queue.
// The zero value is not usable — construct one with New.
type Queue struct {
	mu       sync.Mutex
	waiting  entryHeap              // ordered by priority then sequence
	index    map[EntryID]*heapItem  // fast lookup into waiting
	assigned map[EntryID]Assignment // active assignments
	nextSeq  uint64
	now      func() time.Time
}

// New returns an empty Queue.
func New() *Queue {
	return &Queue{
		index:    make(map[EntryID]*heapItem),
		assigned: make(map[EntryID]Assignment),
		now:      time.Now,
	}
}

// AddEntry adds a new waiting entry with the given priority (rank) and
// returns the resulting EntryAdded event. It returns ErrDuplicateEntry
// if id is already known to the queue, waiting or assigned.
func (q *Queue) AddEntry(id EntryID, priority Priority) (EntryAdded, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, waiting := q.index[id]; waiting {
		return EntryAdded{}, ErrDuplicateEntry
	}
	if _, assigned := q.assigned[id]; assigned {
		return EntryAdded{}, ErrDuplicateEntry
	}

	q.nextSeq++
	seq := q.nextSeq

	entry := &Entry{ID: id, Priority: priority, Sequence: seq, State: Waiting}
	item := &heapItem{entry: entry}
	heap.Push(&q.waiting, item)
	q.index[id] = item

	return EntryAdded{
		EntryID:    id,
		Priority:   priority,
		Sequence:   seq,
		OccurredAt: q.now(),
	}, nil
}

// UpdatePriority changes the priority (rank) of a waiting entry and
// returns the resulting PriorityChanged event. The queue reorders the
// entry accordingly. It returns ErrEntryNotFound if id is unknown, or
// ErrEntryNotWaiting if id has already been assigned.
func (q *Queue) UpdatePriority(id EntryID, newPriority Priority) (PriorityChanged, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	item, waiting := q.index[id]
	if !waiting {
		if _, assigned := q.assigned[id]; assigned {
			return PriorityChanged{}, ErrEntryNotWaiting
		}
		return PriorityChanged{}, ErrEntryNotFound
	}

	old := item.entry.Priority
	item.entry.Priority = newPriority
	heap.Fix(&q.waiting, item.pos)

	return PriorityChanged{
		EntryID:     id,
		OldPriority: old,
		NewPriority: newPriority,
		OccurredAt:  q.now(),
	}, nil
}

// RemoveEntry removes a waiting entry from the queue without assigning
// it, returning the resulting EntryRemoved event. It returns
// ErrEntryNotFound if id is unknown, or ErrEntryNotWaiting if id has
// already been assigned.
func (q *Queue) RemoveEntry(id EntryID) (EntryRemoved, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	item, waiting := q.index[id]
	if !waiting {
		if _, assigned := q.assigned[id]; assigned {
			return EntryRemoved{}, ErrEntryNotWaiting
		}
		return EntryRemoved{}, ErrEntryNotFound
	}

	heap.Remove(&q.waiting, item.pos)
	delete(q.index, id)

	return EntryRemoved{EntryID: id, OccurredAt: q.now()}, nil
}

// AssignNext atomically selects the highest-ranked eligible entry,
// removes it from the active waiting queue, and creates its immutable
// assignment fact in a single operation — closing the race window a
// separate "peek, then assign" API would leave open. It returns
// ErrEmptyQueue if there is no eligible entry.
func (q *Queue) AssignNext(assigneeID AssigneeID) (Assignment, AssignmentCreated, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.waiting.Len() == 0 {
		return Assignment{}, AssignmentCreated{}, ErrEmptyQueue
	}

	item := heap.Pop(&q.waiting).(*heapItem)
	delete(q.index, item.entry.ID)

	item.entry.State = Assigned

	now := q.now()
	assignment := Assignment{
		EntryID:              item.entry.ID,
		AssigneeID:           assigneeID,
		PriorityAtAssignment: item.entry.Priority,
		Sequence:             item.entry.Sequence,
		AssignedAt:           now,
	}
	q.assigned[item.entry.ID] = assignment

	event := AssignmentCreated{
		EntryID:              assignment.EntryID,
		AssigneeID:           assignment.AssigneeID,
		PriorityAtAssignment: assignment.PriorityAtAssignment,
		Sequence:             assignment.Sequence,
		OccurredAt:           now,
	}

	return assignment, event, nil
}

// CancelAssignment undoes an active assignment and returns the entry to
// the waiting queue at its original priority and sequence — as if it
// had never been assigned. It returns ErrEntryNotAssigned if id is not
// currently assigned.
//
// The AssignmentCreated event this cancels is never modified; this
// produces a new AssignmentCancelled event instead, per the engine's
// append-only event history (Q5).
func (q *Queue) CancelAssignment(id EntryID) (AssignmentCancelled, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	assignment, ok := q.assigned[id]
	if !ok {
		return AssignmentCancelled{}, ErrEntryNotAssigned
	}
	delete(q.assigned, id)

	entry := &Entry{
		ID:       assignment.EntryID,
		Priority: assignment.PriorityAtAssignment,
		Sequence: assignment.Sequence,
		State:    Waiting,
	}
	item := &heapItem{entry: entry}
	heap.Push(&q.waiting, item)
	q.index[id] = item

	return AssignmentCancelled{
		EntryID:    assignment.EntryID,
		AssigneeID: assignment.AssigneeID,
		OccurredAt: q.now(),
	}, nil
}

// Peek returns the entry AssignNext would currently select, without
// claiming it. The result can be stale the instant another goroutine
// calls AssignNext or UpdatePriority — it is meant for inspection
// (metrics, display), not for building a "peek, then assign" workflow.
func (q *Queue) Peek() (Entry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.waiting.Len() == 0 {
		return Entry{}, false
	}
	return *q.waiting[0].entry, true
}

// Len returns the number of entries currently waiting (eligible).
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.waiting.Len()
}
