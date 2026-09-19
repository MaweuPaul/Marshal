package queue

// EntryID is an opaque identifier for a queue entry. The engine assigns
// it no meaning beyond identity — what it represents (a patient, a
// ticket, a job) is entirely up to the host application.
type EntryID string

// AssigneeID is an opaque identifier for whoever, or whatever, an entry
// can be assigned to. The engine assigns it no meaning beyond identity.
type AssigneeID string

// State is the lifecycle state of an entry within the engine.
type State int

const (
	// Waiting entries are eligible to be returned by AssignNext.
	Waiting State = iota
	// Assigned entries have an active assignment and are no longer
	// eligible in the active waiting queue.
	Assigned
)

// Entry is the engine's only unit of work. It carries no domain data —
// just enough to order and assign it.
type Entry struct {
	ID       EntryID
	Priority Priority
	Sequence uint64
	State    State
}
