# Ordexa

A deterministic, concurrent, auditable **priority-assignment queue engine**, built in Go, developed around emergency-department triage as its reference use case.

This repository is **not** a patient-management system, hospital application, clinical decision system, or database-backed healthcare platform.

The engine is designed to answer one core question correctly, even under concurrent load:

**Given all currently eligible queue entries, which entry should be assigned next?**

Emergency triage remains the motivating example and explains why this engine exists, but the queue engine itself does **not** store or understand patient information. It operates on opaque entry and assignee identifiers, externally supplied priorities, and queue-relevant facts — nothing more.

> **Status:** Planning stage. Nothing in this repository has been implemented yet. Everything below describes the intended design and current design decisions, not existing functionality. See [Disclaimer](#disclaimer).

---

## Core Idea

The engine accepts **commands** and emits **events**. It does not diagnose, interpret meaning, own domain records, or dictate persistence technology.

```text
External / Host Application
          │
          │ commands
          ▼
┌──────────────────────────┐
│       Queue Engine       │
│                          │
│ ordering                 │
│ invariants               │
│ state transitions        │
│ assignment rules         │
│ concurrency              │
└────────────┬─────────────┘
             │
             │ events
             ▼
     Host Application
             │
             ├── PostgreSQL
             ├── MySQL
             ├── Kafka
             ├── event store
             ├── files
             └── anything else
```

The host application decides what an entry ID represents, what an assignee ID represents, and how (or whether) to persist the events the engine emits. This repository defines queue semantics and event contracts. It does not define storage infrastructure.

---

## Reference Domain: Emergency-Department Triage

Emergency departments do not normally treat patients strictly on a first-come-first-served basis. Patients are clinically assessed by trained healthcare professionals and assigned a triage category representing the urgency of their condition.

The reference model is the **South African Triage Scale (SATS)**, which maps to queue priority as follows:

| SATS Category | General Meaning | Queue Priority |
| -------------- | ---------------- | -------------- |
| Red            | Emergency         | 1              |
| Orange         | Very urgent       | 2              |
| Yellow         | Urgent            | 3              |
| Green          | Routine           | 4              |

A trained clinical professional, or an external clinical system, determines the triage category. The queue engine receives the resulting priority. It does **not** determine why the priority was assigned, does not interpret symptoms, and does not increase priority merely because time has passed.

This mapping lives in a small triage-specific layer (`triage/sats.go`) that sits outside the core queue package — the core `queue` package operates on `Priority`, never on clinical terminology.

---

## Responsibility Boundary

The project deliberately separates **clinical/domain decision-making** (owned by the host application) from **queue ordering and assignment decision-making** (owned by this repository).

```text
             HOST-APPLICATION RESPONSIBILITY

               Domain event occurs
              (e.g. patient arrives,
             condition is reassessed)
                       │
                       ▼
              Trained professional or
               external system acts
                       │
                       │ determines priority
                       ▼

────────────────────────────────────────────────

                QUEUE-ENGINE RESPONSIBILITY

                       │
                       ▼
               Triage Queue Engine
                       │
             ┌─────────┼─────────┐
             │         │         │
             ▼         ▼         ▼
          ordering   state    assignment
                     tracking   safety
                       │
                       ▼
                Next eligible entry
```

### What the queue core should not know

Internally, the core `queue` package is not coupled to patient-specific (or any domain-specific) data. Its central concept is intentionally minimal:

```go
type Entry struct {
    ID       EntryID
    Priority Priority
    Sequence uint64
}
```

The engine only needs concepts such as: entry ID, priority, queue sequence/order, queue state, assignee ID, assignment, ordering rules, and events. The external system decides what those IDs represent.

For example, `EntryID("abc123")` might correspond to a patient in a hospital application — the queue engine does not know that. Similarly, `AssigneeID("xyz789")` might correspond to a doctor, clinician, workstation, or team — the engine treats it as an opaque identifier.

### Priority Is Host-Defined, Not Fixed by the Engine

The engine does **not** dictate a priority scale. It does not decide how many priority levels exist, what they mean, or which values are valid — it only needs `Priority` values to be comparable, so it can order entries consistently and deterministically.

The four-level SATS mapping (Red/Orange/Yellow/Green → 1–4) shown above is one example, supplied by the `triage` reference layer for the emergency-department use case. A different host application could use two levels, ten levels, or a non-numeric ordered scheme entirely — the `queue` core does not care, as long as the host consistently supplies comparable `Priority` values. The engine enforces *ordering*; the host defines *what the priorities mean and how many there are*.

### What the repository must not own

The repository must **not** own:

* patient names, age, diagnosis, symptoms, medical history, or clinical notes
* the reason for a reassessment
* clinician records or doctor names
* hospital records or hospital workflow data
* authentication
* medical-record storage
* a UI
* a specific persistence technology (PostgreSQL, MySQL, MongoDB, Kafka, SQLite, an event store, files, or anything else)

Those belong to the external application integrating the queue.

### What the queue owns

Although the *clinical reason* for a priority change belongs outside the repository, the queue engine does need to know *that* the priority changed, because that affects ordering. For example:

```text
08:04  Entry abc123 added        (priority = 4, sequence = 27)
08:37  Priority changed          (4 → 1)
08:38  Entry assigned            (assignee = doctor-17, priority at assignment = 1, sequence = 27)
```

The queue does **not** need `"Reason: patient condition deteriorated"` — that explanation belongs to the clinical system. The queue merely records the queue-relevant fact:

```text
PriorityChanged
EntryID: abc123
From: 4
To: 1
```

---

## Core Queue Invariants

### Q1 — Higher Priority Cannot Be Bypassed

Given two eligible entries `A` and `B`:

```text
if A has a higher priority than B,
B must not be assigned while A remains eligible.
```

### Q2 — FIFO Within the Same Priority

Where two eligible entries have the same priority, the entry with the earlier queue sequence is selected first.

```text
priority
  → sequence
```

A monotonically increasing queue sequence is used as the tie-breaker rather than relying solely on timestamps, since two entries could theoretically share an identical timestamp. The queue core is deterministic: given identical state and commands, it makes the same ordering decision every time.

### Q3 — An Entry Cannot Be Assigned Twice

An entry may have at most one active assignment. Two concurrent callers requesting the next entry must never receive the same entry:

```text
one queue entry → at most one active assignment
```

### Q4 — Priority Changes Come From Outside the Queue Engine

The queue engine does not independently change an entry's priority because time has elapsed. The host application submits an `UpdatePriority` command; the engine reorders and emits a `PriorityChanged` event. The previous priority remains part of the auditable event history.

### Q5 — Assignment Records Are Immutable

An assignment is an immutable fact. Once an `AssignmentCreated` event has been produced, it is never edited to rewrite history. If something later changes (for example, an assignment needs to be undone), a new event is appended instead:

```text
AssignmentCreated
       ↓
   (later)
       ↓
AssignmentCancelled
```

rather than mutating the original assignment record.

### Q6 — Assigned Entries Leave the Active Queue

Once an entry is successfully assigned, it is no longer eligible to be returned by the active queue:

```text
WAITING
   ↓
ASSIGNED
```

The invariant: an entry with an active assignment must not remain eligible in the waiting queue. This is a removal from active eligibility, not a deletion of history — the assignment and prior events remain available for auditing.

---

## Atomic `AssignNext`

The public API avoids a two-step workflow like:

```go
entry := queue.Next()
queue.Assign(entry, assigneeID)
```

because there is a race window between selecting and assigning the entry — two concurrent callers could select the same entry before either assigns it.

Instead, the engine exposes a single atomic operation:

```go
assignment, events, err := queue.AssignNext(assigneeID)
```

Conceptually, this operation:

1. determines the highest-ranked eligible entry;
2. claims that entry atomically;
3. removes it from active waiting eligibility;
4. creates the immutable assignment fact;
5. returns the resulting event(s).

---

## Commands In, Events Out

```text
COMMAND
   ↓
QUEUE ENGINE
   ↓
EVENT(S)
```

Possible commands (kept intentionally minimal for v1):

```text
AddEntry
UpdatePriority
RemoveEntry
AssignNext
```

Possible emitted events:

```text
EntryAdded
PriorityChanged
EntryRemoved
AssignmentCreated
```

A potential future event: `AssignmentCancelled`. The event set is not over-designed up front — it grows only as real needs emerge.

### Example event shapes

These are illustrative, not a final Go API:

```go
type EntryAdded struct {
    EntryID    EntryID
    Priority   Priority
    Sequence   uint64
    OccurredAt time.Time
}

type PriorityChanged struct {
    EntryID     EntryID
    OldPriority Priority
    NewPriority Priority
    OccurredAt  time.Time
}

type AssignmentCreated struct {
    EntryID              EntryID
    AssigneeID           AssigneeID
    PriorityAtAssignment Priority
    Sequence             uint64
    OccurredAt           time.Time
}
```

---

## Persistence Belongs to the Host

The queue engine does not dictate PostgreSQL, MySQL, MongoDB, Kafka, SQLite, an event store, files, or any other persistence technology. A consuming application does something conceptually like:

```go
events, err := q.UpdatePriority(id, 1)
```

and then, entirely at its own discretion:

```go
repository.Save(events)
// or
kafka.Publish(events)
// or
eventStore.Append(events)
```

The queue does not care which. This repository defines queue semantics and event contracts; it does not define storage infrastructure.

### Event replay (future direction)

Because the queue produces immutable events, a future design may allow rebuilding queue state by replaying previously stored events:

```go
q := queue.New()

for _, event := range storedEvents {
    q.Apply(event)
}
```

This is a **potential future capability**, not a version-1 requirement. This project does not present itself as a complete event-sourced architecture at this stage — the goal for v1 is simple immutable events and a clean integration boundary, not full event sourcing.

### Open integration question (future design work)

External persistence introduces consistency questions that remain open, for example:

```text
1. engine assigns entry
2. event is emitted
3. host crashes before persisting event
```

The eventual integration contract must define how persistence, retries, idempotency, and recovery work. This is deliberately left as future design work rather than solved by baking a specific database into the core.

---

## Concurrency Model

Concurrency is central to this project. `AssignNext` must guarantee that two concurrent callers never receive the same entry, and the in-memory engine will be validated with randomized concurrent load and the Go race detector.

How a host application coordinates `AssignNext` across multiple processes or machines (e.g. multiple servers sharing persisted queue state) is part of the future integration contract described above, not a v1 requirement of the core engine.

---

## Repository Responsibility

### The repository SHOULD own

```text
Entry
EntryID
Priority
Sequence
Ordering
Active queue state
AssignNext
Priority updates
Concurrency safety
Invariants
Immutable queue events
Deterministic behaviour
Testing
```

### The repository SHOULD NOT own

```text
Patient records
Doctor records
Clinical records
Clinical decision making
Reason for reassessment
Database implementation
PostgreSQL
Authentication
HTTP API
UI
Notifications
Hospital workflow
```

Optional adapters or examples may be added later (e.g. a PostgreSQL-backed host, an HTTP API), but they must not contaminate the queue core.

---

## Project Structure — Planned

```text
ordexa/
│
├── queue/
│   ├── queue.go
│   ├── entry.go
│   ├── priority.go
│   ├── assignment.go
│   ├── events.go
│   ├── errors.go
│   └── queue_test.go
│
├── triage/
│   └── sats.go
│
├── examples/
│   └── emergency-department/
│       └── main.go
│
├── docs/
│   └── adr/
│
├── go.mod
├── README.md
└── LICENSE
```

This structure is illustrative rather than final. No code has been written yet.

---

# Roadmap

## Phase 0 — Domain and Invariants

* [ ] Define `Entry`, `Priority`, `Sequence`
* [ ] Define queue eligibility
* [ ] Define assignment semantics
* [ ] Define ordering invariants (Q1–Q6 above)
* [ ] Define immutable event semantics
* [ ] Define the queue/host responsibility boundary
* [ ] Record important design decisions as ADRs

---

## Phase 1 — Pure Deterministic Queue

* [ ] Implement `AddEntry`
* [ ] Implement ordering by priority
* [ ] Implement FIFO/sequence tie-breaking
* [ ] Implement `Peek`, if useful
* [ ] Implement `RemoveEntry`
* [ ] Unit tests for ordering rules

No database. No API. No HTTP.

---

## Phase 2 — Priority Changes

* [ ] Implement `UpdatePriority`
* [ ] Implement queue reordering
* [ ] Emit `PriorityChanged` events
* [ ] Test promotion and demotion
* [ ] Ensure no duplicate queue entries are created

---

## Phase 3 — Assignment

* [ ] Implement atomic `AssignNext`
* [ ] Remove assigned entries from the active queue
* [ ] Emit immutable `AssignmentCreated` events
* [ ] Prevent duplicate assignment
* [ ] Concurrency tests

---

## Phase 4 — In-Memory Concurrency

* [ ] Test simultaneous `AssignNext` calls
* [ ] Verify synchronization correctness
* [ ] Randomized load testing
* [ ] Go race detector (`go test -race`)
* [ ] Verify queue invariants hold under load

---

## Phase 5 — Event and Recovery Contract

* [ ] Define immutable event interfaces
* [ ] Define event application/replay, if appropriate
* [ ] Define idempotency semantics
* [ ] Define command retry behavior
* [ ] Define host persistence expectations

---

## Phase 6 — Integration Examples

Only after the core is solid, demonstrate how an external application might:

* [ ] persist events
* [ ] expose HTTP endpoints
* [ ] use PostgreSQL
* [ ] integrate with a clinical system

These are examples/adapters, not requirements of the queue core.

---

## Possible Future Work

The following ideas are deliberately outside the first implementation:

* queue-aging policies;
* waiting-time alerts;
* configurable institutional queue policies;
* multiple treatment areas or specialty-specific queues;
* clinician/assignee availability and resource constraints;
* distributed multi-instance coordination;
* event-sourced persistence and full replay support;
* queue-policy simulation and comparison.

Any feature that would let the queue engine interpret or infer domain-specific urgency (e.g. clinical urgency) on its own must remain clearly out of scope for the core — priorities are always supplied by the host application.

---

## Running It

No runnable code exists yet. Instructions will be added once the first implementation phase is complete.

---

## Disclaimer

This repository is an engineering project exploring a deterministic, concurrent, auditable priority-assignment queue engine, using emergency-department triage as the driving example.

It is **not a medical device** and is not a patient-management, hospital, or clinical decision system.

It does not diagnose, does not determine clinical urgency, and does not replace the judgment of trained healthcare professionals or any other domain expert. Any priority used by the engine is assumed to have been supplied by an external, appropriately authorized process.

The software described in this repository is not intended for deployment in a clinical environment on its own. A real-world healthcare deployment built on top of this engine would require clinical validation, appropriate governance, security and privacy controls, regulatory review where applicable, integration with institutional workflows, and compliance with relevant healthcare and data-protection requirements — all of which are the responsibility of the host application, not this repository.
