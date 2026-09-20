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

	fmt.Println("\nassignment order:")
	for {
		assignment, _, err := q.AssignNext("doctor-1")
		if err != nil {
			break
		}
		fmt.Printf("  assigned %-10s rank=%d sequence=%d\n",
			assignment.EntryID, assignment.PriorityAtAssignment, assignment.Sequence)
	}
}
