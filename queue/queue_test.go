package queue

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
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
				assignment, _, err := q.AssignNext(AssigneeID(entryID(worker)))
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
		if _, _, err := q.AssignNext(AssigneeID("worker")); err != nil {
			t.Fatalf("step %d: AssignNext() error = %v", i, err)
		}
	}
}

func entryID(i int) EntryID {
	return EntryID(fmt.Sprintf("e%d", i))
}
