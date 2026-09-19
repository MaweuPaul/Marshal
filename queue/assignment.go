package queue

import "time"

// Assignment is an immutable fact: EntryID was assigned to AssigneeID.
// Once created it is never edited — a later change (e.g. a
// cancellation) is expressed as a new event, not a mutation of this
// record.
type Assignment struct {
	EntryID              EntryID
	AssigneeID           AssigneeID
	PriorityAtAssignment Priority
	Sequence             uint64
	AssignedAt           time.Time
}
