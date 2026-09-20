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
	EventSeq   uint64
	EntryID    EntryID
	Priority   Priority
	Sequence   uint64
	OccurredAt time.Time
}

// PriorityChanged is emitted when an entry's priority is updated. The
// queue records that the priority changed and reorders accordingly; it
// does not record, or need to know, why.
type PriorityChanged struct {
	EventSeq    uint64
	EntryID     EntryID
	OldPriority Priority
	NewPriority Priority
	OccurredAt  time.Time
}

// EntryRemoved is emitted when an entry is removed from the queue
// without being assigned.
type EntryRemoved struct {
	EventSeq   uint64
	EntryID    EntryID
	OccurredAt time.Time
}

// AssignmentCreated is emitted when an entry is atomically claimed by
// AssignNext. It mirrors the resulting Assignment as an event.
type AssignmentCreated struct {
	EventSeq             uint64
	EntryID              EntryID
	AssigneeID           AssigneeID
	PriorityAtAssignment Priority
	Sequence             uint64
	OccurredAt           time.Time
}

// AssignmentCancelled is emitted when an active assignment is undone by
// CancelAssignment. The entry returns to the waiting queue at its
// original priority and sequence — as if it had never been assigned —
// rather than losing its place for a mistake that wasn't its own. The
// AssignmentCreated fact this cancels is never edited; this is a new,
// separate event appended after it.
type AssignmentCancelled struct {
	EventSeq   uint64
	EntryID    EntryID
	AssigneeID AssigneeID
	OccurredAt time.Time
}

// AssignmentReassigned is emitted when an active assignment's assignee
// is changed directly by Reassign — a hand-off from one assignee to
// another, with no re-entry into the waiting queue and no risk of a
// higher-priority arrival intercepting the entry in between. The
// AssignmentCreated fact this follows is never edited; this is a new,
// separate event appended after it.
type AssignmentReassigned struct {
	EventSeq      uint64
	EntryID       EntryID
	OldAssigneeID AssigneeID
	NewAssigneeID AssigneeID
	OccurredAt    time.Time
}

func (EntryAdded) isEvent()           {}
func (PriorityChanged) isEvent()      {}
func (EntryRemoved) isEvent()         {}
func (AssignmentCreated) isEvent()    {}
func (AssignmentCancelled) isEvent()  {}
func (AssignmentReassigned) isEvent() {}
