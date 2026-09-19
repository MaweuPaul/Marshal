package queue

import (
	"errors"
	"fmt"
	"sync"
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

	if _, _, err := q.AssignNext(AssigneeID("worker")); err != nil {
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

	if _, _, err := q.AssignNext(AssigneeID("worker")); err != nil {
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

	if _, _, err := q.AssignNext(AssigneeID("worker")); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}

	if _, err := q.RemoveEntry("e1"); !errors.Is(err, ErrEntryNotWaiting) {
		t.Fatalf("RemoveEntry() error = %v, want ErrEntryNotWaiting", err)
	}
}

func TestAssignNextOnEmptyQueue(t *testing.T) {
	q := New()
	if _, _, err := q.AssignNext(AssigneeID("worker")); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("AssignNext() error = %v, want ErrEmptyQueue", err)
	}
}

func TestAssignNextReturnsAssignmentAndEvent(t *testing.T) {
	q := New()
	mustAdd(t, q, "e1", 3)

	assignment, event, err := q.AssignNext(AssigneeID("doctor-1"))
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
	if _, _, err := q.AssignNext(AssigneeID("worker")); err != nil {
		t.Fatalf("AssignNext() error = %v", err)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len() = %d after AssignNext, want 0", got)
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
				assignment, _, err := q.AssignNext(AssigneeID(entryID(worker)))
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
		if _, _, err := q.AssignNext(AssigneeID("worker")); err != nil {
			t.Fatalf("step %d: AssignNext() error = %v", i, err)
		}
	}
}

func entryID(i int) EntryID {
	return EntryID(fmt.Sprintf("e%d", i))
}
