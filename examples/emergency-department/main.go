// Command emergency-department is a runnable illustration of how a
// host application might drive the queue engine, using the South
// African Triage Scale (SATS) as a reference priority scheme. It is an
// example, not part of the queue core — the mapping below is exactly
// the kind of host-side configuration the README describes; the
// queue package never sees these category names.
package main

import (
	"fmt"
	"log"

	"github.com/MaweuPaul/Marshal/queue"
)

// satsRank is a host-side configuration: SATS category -> queue rank.
// Lower rank is more urgent. The queue package knows nothing about
// this map; it only ever receives the resulting queue.Priority.
var satsRank = map[string]queue.Priority{
	"Red":    1,
	"Orange": 2,
	"Yellow": 3,
	"Green":  4,
}

func main() {
	q := queue.New()

	add := func(id queue.EntryID, category string) {
		rank, ok := satsRank[category]
		if !ok {
			log.Fatalf("unknown triage category: %s", category)
		}
		event, err := q.AddEntry(id, rank)
		if err != nil {
			log.Fatalf("AddEntry(%s): %v", id, err)
		}
		fmt.Printf("added    %-10s category=%-6s rank=%d sequence=%d\n",
			event.EntryID, category, event.Priority, event.Sequence)
	}

	add("patient-a", "Green")
	add("patient-b", "Yellow")
	add("patient-c", "Orange")
	add("patient-d", "Red")
	add("patient-e", "Yellow")

	// A clinician reassesses patient-a: Green -> Red.
	change, err := q.UpdatePriority("patient-a", satsRank["Red"])
	if err != nil {
		log.Fatalf("UpdatePriority: %v", err)
	}
	fmt.Printf("reassessed %-8s rank %d -> %d\n", change.EntryID, change.OldPriority, change.NewPriority)

	// AssignNext requires a non-empty commandID identifying this logical
	// attempt: retrying AssignNext isn't safe on its own (a retry would
	// claim a different entry), so replaying the same commandID instead
	// returns the original result. A real host would derive this from
	// its own request/retry identifier; here it's just a counter.
	nextCommandID := func() func() string {
		n := 0
		return func() string {
			n++
			return fmt.Sprintf("cmd-%d", n)
		}
	}()

	// doctor-1 is offered the most urgent patient, but gets pulled away
	// before treating them — the assignment is cancelled. patient-a
	// returns to the waiting queue at its ORIGINAL priority and
	// sequence, so it does not lose its place to patient-d, which is
	// also rank 1 but arrived later.
	first, _, err := q.AssignNext("doctor-1", nextCommandID())
	if err != nil {
		log.Fatalf("AssignNext: %v", err)
	}
	fmt.Printf("assigned   %-8s rank=%d sequence=%d to=%s\n",
		first.EntryID, first.PriorityAtAssignment, first.Sequence, first.AssigneeID)

	cancelled, err := q.CancelAssignment(first.EntryID)
	if err != nil {
		log.Fatalf("CancelAssignment: %v", err)
	}
	fmt.Printf("cancelled  %-8s was assigned to=%s\n", cancelled.EntryID, cancelled.AssigneeID)

	fmt.Println("\nassignment order:")
	for {
		assignment, _, err := q.AssignNext("doctor-2", nextCommandID())
		if err != nil {
			break
		}
		fmt.Printf("  assigned %-10s rank=%d sequence=%d\n",
			assignment.EntryID, assignment.PriorityAtAssignment, assignment.Sequence)
	}
}
