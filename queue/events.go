package queue

import "time"

// Event is an immutable, queue-relevant fact emitted by a command. The
// engine never mutates a past event — state changes are always new
// events, appended to whatever history the host chooses to keep.
type Event interface {
	isEvent()
}

// EntryAdded is emitted when a new entry joins the waiting queue.
type EntryAdded struct {
	EntryID    EntryID
	Priority   Priority
	Sequence   uint64
	OccurredAt time.Time
}

// PriorityChanged is emitted when an entry's priority is updated. The
// queue records that the priority changed and reorders accordingly; it
// does not record, or need to know, why.
type PriorityChanged struct {
	EntryID     EntryID
	OldPriority Priority
	NewPriority Priority
	OccurredAt  time.Time
}

// EntryRemoved is emitted when an entry is removed from the queue
// without being assigned.
type EntryRemoved struct {
	EntryID    EntryID
	OccurredAt time.Time
}

// AssignmentCreated is emitted when an entry is atomically claimed by
// AssignNext. It mirrors the resulting Assignment as an event.
type AssignmentCreated struct {
	EntryID              EntryID
	AssigneeID           AssigneeID
	PriorityAtAssignment Priority
	Sequence             uint64
	OccurredAt           time.Time
}

func (EntryAdded) isEvent()        {}
func (PriorityChanged) isEvent()   {}
func (EntryRemoved) isEvent()      {}
func (AssignmentCreated) isEvent() {}
