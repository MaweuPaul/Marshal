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

Emergency departments do not normally treat patients strictly on a first-come-first-served basis. Patients are clinically assessed by trained healthcare professionals and assigned a triage priority representing the urgency of their condition.

### Priority levels are configuration, not code

The queue is **not** hard-coded to categories such as `Red` / `Orange` / `Yellow` / `Green`, and it does **not** hard-code a specific range such as `1`–`7`. How many priority levels exist, what they're called, and what each one means is **configuration owned by whoever operates the system** — never something baked into the engine.

What the engine consumes is a single ordered value per entry, referred to here as **rank**: a smaller rank means greater urgency.

```text
smaller rank = higher priority
larger rank  = lower priority
```

The engine's only requirement is that rank values support ordering comparison, so entries sort consistently and deterministically. It does not care how many distinct ranks are configured or what they're named.

A human-readable **priority level** — a name plus a rank — is a configuration concept that sits *above* the queue core, typically owned by the host application (or a thin configuration layer such as `triage/`). For example:

```go
type PriorityLevel struct {
    ID     string
    Name   string
    Rank   int
    Active bool
}
```

```json
{ "id": "priority-critical", "name": "Critical", "rank": 1, "active": true }
```

An entry doesn't need to carry a raw number chosen ad hoc — it can reference a configured level, and the engine looks up (or is simply given) the resulting rank:

```text
priority-critical → rank 1
```

Because the engine only ever sees the resulting rank, any organization can configure its own set of levels without touching the engine:

```text
Hospital A                  Hospital B                   Hospital C
Critical    → rank 1        P1 → rank 1                  RED    → rank 1
High        → rank 2        P2 → rank 2                  ORANGE → rank 2
Medium      → rank 3        P3 → rank 3                  YELLOW → rank 3
Low         → rank 4        P4 → rank 4                  GREEN  → rank 4
                             P5 → rank 5
```

No code change is required to add, rename, reorder, or retire a level — that is purely a configuration change in the host application.

A trained clinical professional, or an external clinical system, determines which priority level applies. The queue engine receives the resulting rank. It does **not** determine why that level was assigned, does not interpret symptoms, and does not increase priority merely because time has passed.

The reference **South African Triage Scale (SATS)** mapping is one example of such a configuration, expressed by the `triage` reference layer (`triage/sats.go`) outside the core queue package:

```text
SATS Category    Priority Level    Rank
Red         →    Critical      →   1
Orange      →    Very Urgent   →   2
Yellow      →    Urgent        →   3
Green       →    Routine       →   4
```

The core `queue` package never sees `"Red"`, `"Critical"`, or a `PriorityLevel` — it only ever operates on the resulting rank (an ordered, comparable value), and it never assumes a fixed number of levels or a fixed range.

### Priority stays numeric — other orderable fields stay separate

A priority level's rank should be a simple ordered value (an integer), not a date or a free-form string. Something like `priority = 2026-09-18` doesn't describe urgency — it describes *when*, which is a different concern.

Dates and other orderable attributes (like queue-entry time) are handled as independent fields that participate in ordering *alongside* priority, not folded into the priority value itself:

```text
Priority level: Critical → rank 1
Entry:          priority_level = Critical, queue_entered_at = 2026-09-18 09:30
```

In this engine, that second-order concern is exactly what [queue sequence](#q2--fifo-within-the-same-priority) already provides — a monotonically increasing tie-breaker applied after rank. See [Initial Queue Ordering Policy](#initial-queue-ordering-policy).

### Example: priority changes through reassessment

An entry's priority level may change while it is waiting, because a trained clinician reassesses it — not because time has passed. For example, given the waiting queue:

```text
P101 → rank 1
P102 → rank 3
P103 → rank 5
P104 → rank 6
```

a clinician reassesses `P103`, moving it from a level with rank `5` to one with rank `2`:

```text
P103: rank 5 → rank 2
```

The queue immediately reflects the new rank:

```text
P101 → rank 1
P103 → rank 2
P102 → rank 3
P104 → rank 6
```

We do not manually recompute a number for `P103` — the host changes which priority level applies (e.g. `Routine` → `Critical`), and the corresponding rank flows into the engine via `UpdatePriority`. The clinician changed the priority; the software only enforced the resulting order. The engine does not decide *that* `P103` became more urgent — it only reacts, deterministically, to the command it was given.

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
type Priority int // the rank of a (host-configured) priority level

type Entry struct {
    ID       EntryID
    Priority Priority
    Sequence uint64
}
```

`Priority` is a plain, ordered numeric rank — the engine does not fix how many distinct ranks exist or what range they span, only that lower values sort as more urgent. Any human-readable label, level configuration, or lookup that produces this rank (see [Priority levels are configuration, not code](#priority-levels-are-configuration-not-code)) lives entirely on the host side; `Entry` only ever carries the resulting rank.

The engine only needs concepts such as: entry ID, priority, queue sequence/order, queue state, assignee ID, assignment, ordering rules, and events. The external system decides what those IDs represent.

For example, `EntryID("abc123")` might correspond to a patient in a hospital application — the queue engine does not know that. Similarly, `AssigneeID("xyz789")` might correspond to a doctor, clinician, workstation, or team — the engine treats it as an opaque identifier.

### Priority Is Host-Defined, Not Fixed by the Engine

The engine does not dictate a priority scale, level count, or level meaning — only that `Priority` is an ordered, comparable rank where a smaller value means greater urgency. See [Priority levels are configuration, not code](#priority-levels-are-configuration-not-code) for how host-configured, human-readable priority levels (with names and ranks) map onto this single rank value.

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

### Initial Queue Ordering Policy

For two eligible waiting entries `A` and `B`, the first version uses a simple, fully deterministic rule:

1. Compare priority. Lower numeric priority wins.
2. If both entries have the same priority, compare queue-entry sequence.
3. The entry with the earlier sequence comes first.

A monotonically increasing sequence number is assigned to every entry as it is added, so sequence values are always unique — two entries can never tie on both priority and sequence. This keeps ordering fully deterministic without needing a further tie-breaker.

A monotonically increasing queue sequence is used as the tie-breaker rather than relying solely on timestamps, since two entries could theoretically share an identical timestamp. The queue core is deterministic: given identical state and commands, it makes the same ordering decision every time.

#### Future direction: configurable ordering fields

`rank → sequence` is the entire v1 ordering policy, and it is not over-designed beyond that. A future version could generalize this into a configurable ordering policy — an ordered list of typed fields (`NUMBER`, `STRING`, `DATE`, `DATETIME`, `BOOLEAN`) with a sort direction each, e.g.:

```yaml
ordering:
  - field: priority.rank
    datatype: number
    direction: ascending
  - field: queue_entered_at
    datatype: datetime
    direction: ascending
  - field: entry_id
    datatype: string
    direction: ascending
```

Note this would be a generic **ordering-field** datatype system (for whatever attributes the host wants to sort by), separate from the priority-level concept above — priority stays a single numeric rank either way. This is explicitly **not** a v1 requirement; it's noted here so the v1 design (a fixed two-step comparator) doesn't need to be revisited to support it later.

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

### Example: host-side priority-level configuration (illustrative only)

None of the following belongs to this repository — it's included only to illustrate how a host application might store the priority-level configuration and entry data described in [Priority levels are configuration, not code](#priority-levels-are-configuration-not-code):

```text
priority_levels                       entries
──────────────────────                ──────────────────────
id                                     id
name                                   priority_level_id
rank                                   queue_entered_at
active                                 status
created_at                             ...
updated_at
```

```text
id       name          rank
--------------------------------
uuid-1   Critical        1
uuid-2   Very Urgent     2
uuid-3   Urgent          3
uuid-4   Routine         4
```

A host reassessing an entry changes `entries.priority_level_id` (e.g. `Routine` → `Critical`) and resolves the new rank, which it then passes to the engine's `UpdatePriority` command — the engine never touches this table itself, and this exact schema is one possible host implementation, not a requirement.

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

* [ ] Define `Entry`, `Priority` (rank), `Sequence`
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
