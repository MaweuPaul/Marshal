package queue

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAddEntryOrdersByPriorityThenSequence(t *testing.T) {
	q := New()

	mustAdd(t, q, "green-1", 4)
	mustAdd(t, q, "yellow-1", 3)
	mustAdd(t, q, "orange-1", 2)
	mustAdd(t, q, "red-1", 1)
	mustAdd(t, q, "yellow-2", 3)

	assertDrainOrder(t, q, "red-1", "orange-1", "yellow-1", "yellow-2", "green-1")
}

// TestQueueIsAgnosticToPriorityScheme proves the engine doesn't secretly
// assume a small range like SATS's 1-4. Here priority comes from a
// completely different, unrelated scheme (a support-ticket system with
// wide, sparse ranks) and ordering still behaves identically: lower
// rank first, sequence breaks ties. The queue package has no idea
// these numbers came from ticket severities instead of triage
// categories — that's the point.
func TestQueueIsAgnosticToPriorityScheme(t *testing.T) {
	const (
		critical Priority = 100
		high     Priority = 250
		normal   Priority = 500
		low      Priority = 999
	)

	q := New()
	mustAdd(t, q, "ticket-low-1", low)
	mustAdd(t, q, "ticket-normal-1", normal)
	mustAdd(t, q, "ticket-high-1", high)
	mustAdd(t, q, "ticket-critical-1", critical)
	mustAdd(t, q, "ticket-normal-2", normal) // same rank as ticket-normal-1, added later

	assertDrainOrder(t, q,
		"ticket-critical-1",
		"ticket-high-1",
		"ticket-normal-1", // earlier sequence than ticket-normal-2, same rank
		"ticket-normal-2",
		"ticket-low-1",
	)
}

func TestUpdatePriorityPromotionReorders(t *testing.T) {
	q := New()

	mustAdd(t, q, "p101", 1)
	mustAdd(t, q, "p102", 3)
	mustAdd(t, q, "p103", 5)
	mustAdd(t, q, "p104", 6)

	// Promotion: p103 becomes more urgent (5 -> 2) and should move up.
	change, err := q.UpdatePriority("p103", 2)
	if err != nil {
		t.Fatalf("UpdatePriority() error = %v", err)
	}
	if change.OldPriority != 5 || change.NewPriority != 2 {
		t.Fatalf("PriorityChanged = %+v, want OldPriority=5 NewPriority=2", change)
	}

	assertDrainOrder(t, q, "p101", "p103", "p102", "p104")
}

func TestUpdatePriorityDemotionReorders(t *testing.T) {
	q := New()

	mustAdd(t, q, "p101", 1)
	mustAdd(t, q, "p102", 3)
	mustAdd(t, q, "p103", 5)
	mustAdd(t, q, "p104", 6)

	// Demotion: p102 becomes less urgent (3 -> 7) and should move down,
	// behind everything that was previously lower priority than it.
	change, err := q.UpdatePriority("p102", 7)
	if err != nil {
		t.Fatalf("UpdatePriority() error = %v", err)
	}
	if change.OldPriority != 3 || change.NewPriority != 7 {
		t.Fatalf("PriorityChanged = %+v, want OldPriority=3 NewPriority=7", change)
	}

	assertDrainOrder(t, q, "p101", "p103", "p104", "p102")
}

func TestUpdatePriorityDoesNotCreateDuplicateEntries(t *testing.T) {
	q := New()

	mustAdd(t, q, "e1", 5)
	mustAdd(t, q, "e2", 5)

	if got := q.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}

	// Multiple reassessments of the same entry must not add extra
	// entries to the queue — only its position should change.
	for _, p := range []Priority{1, 9, 3, 1} {
		if _, err := q.UpdatePriority("e1", p); err != nil {
			t.Fatalf("UpdatePriority(e1, %d) error = %v", p, err)
		}
		if got := q.Len(); got != 2 {
			t.Fatalf("Len() = %d after UpdatePriority(e1, %d), want 2", got, p)
		}
	}

	if _, err := q.AddEntry("e1", 1); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("AddEntry(e1) after reassessments error = %v, want ErrDuplicateEntry", err)
	}
}

func TestUpdatePriorityOnAssignedEntryFails(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}

	if _, err := q.UpdatePriority("e1", 5); !errors.Is(err, ErrEntryNotWaiting) {
		t.Fatalf("UpdatePriority() error = %v, want ErrEntryNotWaiting", err)
	}
}

func TestUpdatePriorityUnknownEntryFails(t *testing.T) {
	q := New()
	if _, err := q.UpdatePriority("ghost", 1); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("UpdatePriority() error = %v, want ErrEntryNotFound", err)
	}
}

func TestAddEntryRejectsDuplicates(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, err := q.AddEntry("e1", 2); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("AddEntry() error = %v, want ErrDuplicateEntry", err)
	}

	if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if _, err := q.AddEntry("e1", 2); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("AddEntry() after assignment error = %v, want ErrDuplicateEntry", err)
	}
}

func TestRemoveEntry(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)
	mustAdd(t, q, "e2", 2)

	if _, err := q.RemoveEntry("e1"); err != nil {
		t.Fatalf("RemoveEntry() error = %v", err)
	}

	entry, ok := q.Peek()
	if !ok || entry.ID != "e2" {
		t.Fatalf("Peek() = %+v, %v, want e2, true", entry, ok)
	}

	if _, err := q.RemoveEntry("e1"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("RemoveEntry() error = %v, want ErrEntryNotFound", err)
	}
}

func TestRemoveEntryOnAssignedEntryFails(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}

	if _, err := q.RemoveEntry("e1"); !errors.Is(err, ErrEntryNotWaiting) {
		t.Fatalf("RemoveEntry() error = %v, want ErrEntryNotWaiting", err)
	}
}

func TestAssignNextOnEmptyQueue(t *testing.T) {
	q := New()
	if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("AssignNext() error = %v, want ErrEmptyQueue", err)
	}
}

func TestAssignNextReturnsAssignmentAndEvent(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 3)

	assignment, event, err := q.AssignNext(AssigneeID("doctor-1"), nextCommandID())
	if err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}

	if assignment.EntryID != "e1" || assignment.AssigneeID != "doctor-1" || assignment.PriorityAtAssignment != 3 {
		t.Fatalf("AssignNext() assignment = %+v, want EntryID=e1 AssigneeID=doctor-1 Priority=3", assignment)
	}
	if event.EntryID != assignment.EntryID || event.AssigneeID != assignment.AssigneeID ||
		event.PriorityAtAssignment != assignment.PriorityAtAssignment || event.Sequence != assignment.Sequence {
		t.Fatalf("AssignmentCreated = %+v does not mirror Assignment = %+v", event, assignment)
	}
}

func TestAssignNextRemovesFromWaiting(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if got := q.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len() = %d after AssignNext, want 0", got)
	}
}

func TestAssignNextRejectsEmptyCommandID(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, _, err := q.AssignNext(AssigneeID("worker"), ""); !errors.Is(err, ErrEmptyCommandID) {
		t.Fatalf("AssignNext() error = %v, want ErrEmptyCommandID", err)
	}
	// The entry must still be waiting -- a rejected command must not
	// have any side effect.
	if got := q.Len(); got != 1 {
		t.Fatalf("Len() = %d after rejected AssignNext, want 1", got)
	}
}

// TestAssignNextIsIdempotentForSameCommandID is the core guarantee ADR
// 0003 requires: replaying the same commandID must return the exact
// original result, not claim a second entry. This is what makes
// AssignNext safe to retry after a host-side timeout or crash-recovery
// resend, within the process's lifetime.
func TestAssignNextIsIdempotentForSameCommandID(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)
	mustAdd(t, q, "e2", 2)

	const cmd = "retry-me"

	first, firstEvent, err := q.AssignNext(AssigneeID("worker"), cmd)
	if err != nil {
		t.Fatalf("first AssignNext() error = %v", err)
	}

	// Retry with the SAME commandID, as if the caller never saw the
	// first response and resent the request.
	second, secondEvent, err := q.AssignNext(AssigneeID("worker"), cmd)
	if err != nil {
		t.Fatalf("retried AssignNext() error = %v", err)
	}

	if second != first {
		t.Fatalf("retried AssignNext() = %+v, want identical to first %+v", second, first)
	}
	if secondEvent != firstEvent {
		t.Fatalf("retried AssignmentCreated = %+v, want identical to first %+v", secondEvent, firstEvent)
	}

	// e2 must still be waiting -- the retry must not have claimed it.
	if got := q.Len(); got != 1 {
		t.Fatalf("Len() = %d after retried AssignNext, want 1 (e2 still waiting)", got)
	}
	entry, ok := q.Peek()
	if !ok || entry.ID != "e2" {
		t.Fatalf("Peek() = %+v, %v, want e2, true", entry, ok)
	}
}

// TestAssignNextCommandCacheEvictsOldestOnceBoundExceeded proves the
// idempotency cache is actually bounded, not an unbounded map masquerading
// as one. It temporarily shrinks maxCommandHistory so the eviction can be
// triggered without needing thousands of calls.
func TestAssignNextCommandCacheEvictsOldestOnceBoundExceeded(t *testing.T) {
	old := maxCommandHistory
	maxCommandHistory = 2
	defer func() { maxCommandHistory = old }()

	q := New()
	mustAdd(t, q, "e1", 1)
	mustAdd(t, q, "e2", 2)
	mustAdd(t, q, "e3", 3)
	mustAdd(t, q, "e4", 4)

	first, _, err := q.AssignNext(AssigneeID("worker"), "cmd-1") // claims e1
	if err != nil {
		t.Fatalf("AssignNext(cmd-1) error = %v", err)
	}
	if _, _, err := q.AssignNext(AssigneeID("worker"), "cmd-2"); err != nil { // claims e2
		t.Fatalf("AssignNext(cmd-2) error = %v", err)
	}
	// This third distinct commandID pushes the cache over its (shrunk)
	// bound of 2, evicting cmd-1's cached result.
	if _, _, err := q.AssignNext(AssigneeID("worker"), "cmd-3"); err != nil { // claims e3
		t.Fatalf("AssignNext(cmd-3) error = %v", err)
	}

	// cmd-1 has now been evicted -- replaying it must be treated as a
	// NEW command (claims e4), not return the stale cached result for e1.
	retried, _, err := q.AssignNext(AssigneeID("worker"), "cmd-1")
	if err != nil {
		t.Fatalf("AssignNext(cmd-1) after eviction error = %v", err)
	}
	if retried.EntryID == first.EntryID {
		t.Fatalf("AssignNext(cmd-1) after eviction returned stale cached entry %s, want a new claim", retried.EntryID)
	}
	if retried.EntryID != "e4" {
		t.Fatalf("AssignNext(cmd-1) after eviction claimed %s, want e4", retried.EntryID)
	}
}

func TestAssignNextDifferentCommandIDsClaimDifferentEntries(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)
	mustAdd(t, q, "e2", 2)

	first, _, err := q.AssignNext(AssigneeID("worker"), "cmd-a")
	if err != nil {
		t.Fatalf("first AssignNext() error = %v", err)
	}
	second, _, err := q.AssignNext(AssigneeID("worker"), "cmd-b")
	if err != nil {
		t.Fatalf("second AssignNext() error = %v", err)
	}

	if first.EntryID == second.EntryID {
		t.Fatalf("both commands claimed %s, want two distinct entries", first.EntryID)
	}
}

func TestCancelAssignmentReturnsEntryToWaiting(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 5)

	assignment, _, err := q.AssignNext(AssigneeID("doctor-1"), nextCommandID())
	if err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len() = %d after AssignNext, want 0", got)
	}

	event, err := q.CancelAssignment("e1")
	if err != nil {
		t.Fatalf("CancelAssignment() error = %v", err)
	}
	if event.EntryID != "e1" || event.AssigneeID != "doctor-1" {
		t.Fatalf("AssignmentCancelled = %+v, want EntryID=e1 AssigneeID=doctor-1", event)
	}

	if got := q.Len(); got != 1 {
		t.Fatalf("Len() = %d after CancelAssignment, want 1", got)
	}
	entry, ok := q.Peek()
	if !ok {
		t.Fatal("Peek() returned no entry after CancelAssignment")
	}
	if entry.ID != "e1" || entry.Priority != 5 || entry.Sequence != assignment.Sequence {
		t.Fatalf("Peek() = %+v, want ID=e1 Priority=5 Sequence=%d", entry, assignment.Sequence)
	}
}

// TestCancelAssignmentPreservesOriginalQueuePosition proves cancellation
// restores an entry to where it would be had it never been assigned,
// not to the back of the line — a cancelled assignment isn't the
// entry's fault, so it must not lose its place to entries that arrived
// later.
func TestCancelAssignmentPreservesOriginalQueuePosition(t *testing.T) {
	q := New()
	mustAdd(t, q, "a", 1) // will be assigned, then cancelled
	mustAdd(t, q, "b", 1) // same priority, arrived after a

	if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}

	// While "a" is assigned, a new same-priority entry arrives.
	mustAdd(t, q, "c", 1)

	if _, err := q.CancelAssignment("a"); err != nil {
		t.Fatalf("CancelAssignment() error = %v", err)
	}

	// "a" must come back ahead of "b" and "c": it arrived before both.
	assertDrainOrder(t, q, "a", "b", "c")
}

func TestCancelAssignmentOnWaitingEntryFails(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, err := q.CancelAssignment("e1"); !errors.Is(err, ErrEntryNotAssigned) {
		t.Fatalf("CancelAssignment() error = %v, want ErrEntryNotAssigned", err)
	}
}

func TestCancelAssignmentOnUnknownEntryFails(t *testing.T) {
	q := New()
	if _, err := q.CancelAssignment("ghost"); !errors.Is(err, ErrEntryNotAssigned) {
		t.Fatalf("CancelAssignment() error = %v, want ErrEntryNotAssigned", err)
	}
}

func TestCancelAssignmentTwiceFails(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if _, err := q.CancelAssignment("e1"); err != nil {
		t.Fatalf("first CancelAssignment() error = %v", err)
	}
	if _, err := q.CancelAssignment("e1"); !errors.Is(err, ErrEntryNotAssigned) {
		t.Fatalf("second CancelAssignment() error = %v, want ErrEntryNotAssigned", err)
	}
}

func TestCancelledEntryCanBeReassignedAndRecancelled(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, _, err := q.AssignNext(AssigneeID("doctor-1"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if _, err := q.CancelAssignment("e1"); err != nil {
		t.Fatalf("CancelAssignment() error = %v", err)
	}

	assignment, _, err := q.AssignNext(AssigneeID("doctor-2"), nextCommandID())
	if err != nil {
		t.Fatalf("second AssignNext() error = %v", err)
	}
	if assignment.EntryID != "e1" || assignment.AssigneeID != "doctor-2" {
		t.Fatalf("AssignNext() = %+v, want EntryID=e1 AssigneeID=doctor-2", assignment)
	}
	if _, err := q.CancelAssignment("e1"); err != nil {
		t.Fatalf("second CancelAssignment() error = %v", err)
	}
}

func TestReassignTransfersAssignee(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, _, err := q.AssignNext(AssigneeID("doctor-1"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}

	event, err := q.Reassign("e1", AssigneeID("doctor-2"))
	if err != nil {
		t.Fatalf("Reassign() error = %v", err)
	}
	if event.EntryID != "e1" || event.OldAssigneeID != "doctor-1" || event.NewAssigneeID != "doctor-2" {
		t.Fatalf("AssignmentReassigned = %+v, want EntryID=e1 OldAssigneeID=doctor-1 NewAssigneeID=doctor-2", event)
	}
}

// TestReassignDoesNotTouchTheWaitingQueue is the whole point of
// Reassign existing: unlike CancelAssignment followed by AssignNext, a
// direct hand-off must never risk a higher-priority arrival
// intercepting the entry. The waiting queue must be completely
// unaffected by a Reassign call.
func TestReassignDoesNotTouchTheWaitingQueue(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 5)
	mustAdd(t, q, "e2", 3)

	if _, _, err := q.AssignNext(AssigneeID("doctor-1"), nextCommandID()); err != nil { // claims e2 (rank 3)
		t.Fatalf("AssignNext() error = %v", err)
	}

	if _, err := q.Reassign("e2", AssigneeID("doctor-2")); err != nil {
		t.Fatalf("Reassign() error = %v", err)
	}

	if got := q.Len(); got != 1 {
		t.Fatalf("Len() = %d after Reassign, want 1 (e1 still waiting)", got)
	}
	entry, ok := q.Peek()
	if !ok || entry.ID != "e1" {
		t.Fatalf("Peek() = %+v, %v, want e1, true", entry, ok)
	}

	// A subsequent AssignNext must still give out e1, proving e2 never
	// went anywhere near the waiting heap.
	next, _, err := q.AssignNext(AssigneeID("doctor-3"), nextCommandID())
	if err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if next.EntryID != "e1" {
		t.Fatalf("AssignNext() = %+v, want EntryID=e1", next)
	}
}

func TestReassignOnWaitingEntryFails(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, err := q.Reassign("e1", AssigneeID("doctor-1")); !errors.Is(err, ErrEntryNotAssigned) {
		t.Fatalf("Reassign() error = %v, want ErrEntryNotAssigned", err)
	}
}

func TestReassignOnUnknownEntryFails(t *testing.T) {
	q := New()
	if _, err := q.Reassign("ghost", AssigneeID("doctor-1")); !errors.Is(err, ErrEntryNotAssigned) {
		t.Fatalf("Reassign() error = %v, want ErrEntryNotAssigned", err)
	}
}

func TestReassignAfterCancelFails(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 1)

	if _, _, err := q.AssignNext(AssigneeID("doctor-1"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if _, err := q.CancelAssignment("e1"); err != nil {
		t.Fatalf("CancelAssignment() error = %v", err)
	}

	if _, err := q.Reassign("e1", AssigneeID("doctor-2")); !errors.Is(err, ErrEntryNotAssigned) {
		t.Fatalf("Reassign() after cancel error = %v, want ErrEntryNotAssigned", err)
	}
}

// TestReassignFixesTheHandoffBug is the exact scenario that motivated
// Reassign: hand off an assigned entry while a higher-priority entry
// arrives in the meantime. CancelAssignment+AssignNext would give the
// new assignee the wrong entry; Reassign must not.
func TestReassignFixesTheHandoffBug(t *testing.T) {
	q := New()
	mustAdd(t, q, "patient-a", 1)

	if _, _, err := q.AssignNext(AssigneeID("doctor-1"), nextCommandID()); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}

	// A more urgent patient arrives while patient-a is assigned.
	mustAdd(t, q, "patient-x", 0)

	event, err := q.Reassign("patient-a", AssigneeID("doctor-2"))
	if err != nil {
		t.Fatalf("Reassign() error = %v", err)
	}
	if event.EntryID != "patient-a" {
		t.Fatalf("Reassign() handed off %s, want patient-a", event.EntryID)
	}

	// patient-x must still be untouched, waiting, unaffected by the
	// hand-off of an unrelated already-assigned entry.
	if got := q.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 (patient-x still waiting)", got)
	}
	entry, ok := q.Peek()
	if !ok || entry.ID != "patient-x" {
		t.Fatalf("Peek() = %+v, %v, want patient-x, true", entry, ok)
	}
}

// TestEventSeqIsMonotonicAcrossEventTypes proves EventSeq is a single,
// strictly increasing counter across the whole queue -- not per event
// type, and not the same thing as Entry.Sequence (which only tracks
// arrival order). Every command that emits an event should advance it.
func TestEventSeqIsMonotonicAcrossEventTypes(t *testing.T) {
	q := New()

	added, err := q.AddEntry("e1", 1)
	if err != nil {
		t.Fatalf("AddEntry() error = %v", err)
	}
	changed, err := q.UpdatePriority("e1", 2)
	if err != nil {
		t.Fatalf("UpdatePriority() error = %v", err)
	}
	_, assigned, err := q.AssignNext(AssigneeID("worker"), nextCommandID())
	if err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	cancelled, err := q.CancelAssignment("e1")
	if err != nil {
		t.Fatalf("CancelAssignment() error = %v", err)
	}
	_, reassigned, err := q.AssignNext(AssigneeID("worker"), nextCommandID())
	if err != nil {
		t.Fatalf("second AssignNext() error = %v", err)
	}

	seqs := []uint64{added.EventSeq, changed.EventSeq, assigned.EventSeq, cancelled.EventSeq, reassigned.EventSeq}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("EventSeq not strictly increasing: %v", seqs)
		}
	}
}

// TestAssignNextConcurrentCallersNeverDuplicate is the core concurrency
// invariant: two callers racing on AssignNext must never receive the
// same entry. Run with -race.
func TestAssignNextConcurrentCallersNeverDuplicate(t *testing.T) {
	q := New()
	const n = 500
	for i := 0; i < n; i++ {
		mustAdd(t, q, entryID(i), Priority(i%7))
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed = make(map[EntryID]int)
	)

	workers := 20
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(worker int) {
			defer wg.Done()
			for {
				assignment, _, err := q.AssignNext(AssigneeID(entryID(worker)), nextCommandID())
				if errors.Is(err, ErrEmptyQueue) {
					return
				}
				if err != nil {
					t.Errorf("AssignNext() unexpected error = %v", err)
					return
				}
				mu.Lock()
				claimed[assignment.EntryID]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	if len(claimed) != n {
		t.Fatalf("claimed %d distinct entries, want %d", len(claimed), n)
	}
	for id, count := range claimed {
		if count != 1 {
			t.Fatalf("entry %s claimed %d times, want exactly 1", id, count)
		}
	}
	if q.Len() != 0 {
		t.Fatalf("Len() = %d after draining, want 0", q.Len())
	}
}

// TestRandomizedConcurrentLoad hammers the queue with many goroutines
// doing a random mix of adds, priority updates, and assignments, with
// randomized timing between operations. It doesn't assert a specific
// order (random input makes that meaningless) — it asserts the
// invariants that must hold no matter what order things happened in:
// every entry is claimed at most once, every added entry is eventually
// claimed, and the queue ends up empty.
func TestRandomizedConcurrentLoad(t *testing.T) {
	q := New()
	// Deliberately using the package-level rand.Intn (not a private
	// rand.New(...) instance) here: the top-level functions share a
	// source guarded by an internal mutex, so they're safe to call
	// from many goroutines at once. A private *rand.Rand is not.

	const n = 1000
	const addWorkers = 10
	const assignWorkers = 15

	var (
		wg       sync.WaitGroup
		addedMu  sync.Mutex
		added    []EntryID
		claimMu  sync.Mutex
		claimed  = make(map[EntryID]int)
		addStart sync.WaitGroup
	)
	addStart.Add(1)

	// Concurrently add n entries with random priorities and random
	// jitter between them, so the order and timing of arrivals varies
	// from run to run.
	wg.Add(addWorkers)
	perWorker := n / addWorkers
	for w := 0; w < addWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			<-addStartDone(&addStart)
			for i := 0; i < perWorker; i++ {
				id := entryID(worker*perWorker + i)
				priority := Priority(rand.Intn(7))
				if _, err := q.AddEntry(id, priority); err != nil {
					t.Errorf("AddEntry(%s, %d) error = %v", id, priority, err)
					continue
				}
				addedMu.Lock()
				added = append(added, id)
				addedMu.Unlock()
				if rand.Intn(4) == 0 {
					runtime.Gosched()
				}
			}
		}(w)
	}
	addStart.Done()

	// Concurrently reassess random already-added entries while adding
	// is still happening — a real host could reassess an entry the
	// instant after it arrives.
	stopReassessing := make(chan struct{})
	var reassessWg sync.WaitGroup
	reassessWg.Add(1)
	go func() {
		defer reassessWg.Done()
		for {
			select {
			case <-stopReassessing:
				return
			default:
			}
			addedMu.Lock()
			n := len(added)
			var id EntryID
			if n > 0 {
				id = added[rand.Intn(n)]
			}
			addedMu.Unlock()
			if id != "" {
				// Ignore the error: id may have already been assigned
				// by the time this runs, which is a valid outcome, not
				// a bug.
				_, _ = q.UpdatePriority(id, Priority(rand.Intn(7)))
			}
			if rand.Intn(3) == 0 {
				runtime.Gosched()
			}
		}
	}()

	// Concurrently drain via AssignNext until every added entry has
	// been claimed exactly once.
	wg.Add(assignWorkers)
	for w := 0; w < assignWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			for {
				claimMu.Lock()
				done := len(claimed) >= n
				claimMu.Unlock()
				if done {
					return
				}
				assignment, _, err := q.AssignNext(AssigneeID(entryID(worker)), nextCommandID())
				if errors.Is(err, ErrEmptyQueue) {
					runtime.Gosched()
					continue
				}
				if err != nil {
					t.Errorf("AssignNext() unexpected error = %v", err)
					return
				}
				claimMu.Lock()
				claimed[assignment.EntryID]++
				claimMu.Unlock()
			}
		}(w)
	}

	wg.Wait()
	close(stopReassessing)
	reassessWg.Wait()

	if len(claimed) != n {
		t.Fatalf("claimed %d distinct entries, want %d", len(claimed), n)
	}
	for id, count := range claimed {
		if count != 1 {
			t.Fatalf("entry %s claimed %d times, want exactly 1", id, count)
		}
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len() = %d after draining, want 0", got)
	}
}

// addStartDone returns a channel that closes once wg's Done has been
// called, letting add-worker goroutines all start racing at once
// instead of trickling in as they're spawned.
func addStartDone(wg *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func mustAdd(t *testing.T, q *Queue, id EntryID, priority Priority) {
	t.Helper()
	if _, err := q.AddEntry(id, priority); err != nil {
		t.Fatalf("AddEntry(%s, %d) error = %v", id, priority, err)
	}
}

// assertDrainOrder repeatedly calls AssignNext and checks that entries
// come out in exactly the given order, draining the queue in the
// process.
func assertDrainOrder(t *testing.T, q *Queue, want ...EntryID) {
	t.Helper()
	for i, id := range want {
		entry, ok := q.Peek()
		if !ok {
			t.Fatalf("step %d: Peek() returned no entry, want %s", i, id)
		}
		if entry.ID != id {
			t.Fatalf("step %d: Peek() = %s, want %s", i, entry.ID, id)
		}
		if _, _, err := q.AssignNext(AssigneeID("worker"), nextCommandID()); err != nil {
			t.Fatalf("step %d: AssignNext() error = %v", i, err)
		}
	}
}

func entryID(i int) EntryID {
	return EntryID(fmt.Sprintf("e%d", i))
}

// nextCommandID returns a fresh, unique commandID for tests that don't
// care about AssignNext's idempotency behavior specifically — safe to
// call concurrently from many goroutines.
var testCmdSeq uint64

func nextCommandID() string {
	return fmt.Sprintf("cmd-%d", atomic.AddUint64(&testCmdSeq, 1))
}
