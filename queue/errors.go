package queue

import "errors"

var (
	// ErrDuplicateEntry is returned by AddEntry when the given EntryID
	// already exists in the queue (waiting or assigned).
	ErrDuplicateEntry = errors.New("queue: entry already exists")

	// ErrEntryNotFound is returned when an operation references an
	// EntryID that isn't currently known to the queue.
	ErrEntryNotFound = errors.New("queue: entry not found")

	// ErrEntryNotWaiting is returned when an operation that requires a
	// waiting entry (e.g. UpdatePriority, RemoveEntry) targets an entry
	// that has already been assigned.
	ErrEntryNotWaiting = errors.New("queue: entry is not waiting")

	// ErrEmptyQueue is returned by AssignNext when there is no eligible
	// waiting entry to assign.
	ErrEmptyQueue = errors.New("queue: no eligible entries")

	// ErrEntryNotAssigned is returned by CancelAssignment and Reassign
	// when the given EntryID is not currently assigned (it may be
	// waiting, or unknown to the queue entirely).
	ErrEntryNotAssigned = errors.New("queue: entry is not assigned")

	// ErrEmptyCommandID is returned by AssignNext when commandID is
	// empty. AssignNext requires a non-empty commandID because retrying
	// it isn't safe on its own — a retry claims a different entry
	// rather than repeating the previous effect. See ADR 0003.
	ErrEmptyCommandID = errors.New("queue: commandID must not be empty")
)
