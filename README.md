# Triage Queue

A concurrent, auditable priority-queue engine for emergency-department patient assignment, built in Go.

The system is designed to answer one core question correctly, even under concurrent load:

**Given all eligible patients currently waiting, which patient should be assigned next?**

The system does **not** determine a patient's medical condition or assign a clinical triage category. That responsibility belongs to trained healthcare professionals using an established clinical triage process.

Once a triage category has been assigned, this system is responsible for maintaining the queue, enforcing the ordering rules, handling reassessments supplied by clinical staff, and ensuring that the same patient cannot be assigned to multiple clinicians simultaneously.

> **Status:** Planning stage. Nothing in this repository has been implemented yet. Everything below describes the intended design and current design decisions, not existing functionality. See [Disclaimer](#disclaimer).

---

## Problem Statement

Emergency departments do not normally treat patients strictly on a first-come-first-served basis.

Patients are clinically assessed by trained healthcare professionals and assigned a triage category representing the urgency of their condition.

For this project, the intended reference model is the **South African Triage Scale (SATS)**, which uses four principal clinical urgency categories:

| Priority | SATS Category | General Queue Meaning |
| -------- | ------------- | --------------------- |
| 1        | Red           | Emergency             |
| 2        | Orange        | Very urgent           |
| 3        | Yellow        | Urgent                |
| 4        | Green         | Routine               |

The clinical category is supplied to the system by a trained person.

The queue engine does **not** independently diagnose patients, calculate medical severity, or promote a patient to a more urgent clinical category merely because time has passed.

Its responsibility begins **after clinical triage**.

At its simplest, the queue follows two ordering rules:

1. **Clinical priority first** — a patient in a more urgent triage category must be considered before an eligible patient in a less urgent category.
2. **Arrival order within the same category** — where two eligible patients have the same triage category, the patient who entered the queue earlier is considered first.

For example:

```text
Patient A → Green   → arrived 08:00
Patient B → Yellow  → arrived 08:10
Patient C → Orange  → arrived 08:40
Patient D → Red     → arrived 09:05
Patient E → Yellow  → arrived 07:50
```

The expected queue order is:

```text
1. Patient D → Red
2. Patient C → Orange
3. Patient E → Yellow
4. Patient B → Yellow
5. Patient A → Green
```

A Green patient who has waited longer does not automatically become Red, Orange, or Yellow.

If a patient's clinical condition changes, a trained healthcare professional performs a reassessment and submits the updated triage category to the system. The queue engine then reorders the patient according to the new externally supplied category.

The engineering challenge is therefore not to perform clinical triage, but to maintain this ordering correctly under real operational conditions such as:

* multiple clinicians requesting patients concurrently;
* new patients entering the queue;
* clinical reassessments changing an existing patient's category;
* patients moving through different workflow states;
* application or database failures;
* repeated requests caused by network failures; and
* the need to reconstruct why a particular assignment occurred.

---

## Responsibility Boundary

The project deliberately separates **clinical decision-making** from **queue decision-making**.

```text
             CLINICAL RESPONSIBILITY

                 Patient arrives
                       │
                       ▼
            Trained triage professional
                       │
                       │ assesses patient
                       ▼
             Triage category assigned
                       │
                       ▼

────────────────────────────────────────────────

              SOFTWARE RESPONSIBILITY

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
                Next eligible patient
```

The software may store and act upon a triage category, but it must not present itself as the authority that clinically assigned that category.

---

## Core Queue Invariants

The correctness of the system will be defined primarily through invariants rather than individual examples.

### Q1 — Higher Clinical Priority Cannot Be Bypassed

Given two patients `A` and `B` who are both waiting and eligible for assignment:

```text
if A has a higher clinical triage priority than B,
B must not be assigned while A remains eligible.
```

For the current SATS-based model:

```text
Red > Orange > Yellow > Green
```

Therefore:

```text
Red    cannot be bypassed by Orange, Yellow, or Green
Orange cannot be bypassed by Yellow or Green
Yellow cannot be bypassed by Green
```

---

### Q2 — FIFO Within the Same Triage Category

Where two eligible patients have the same triage category:

```text
the patient who entered the queue earlier is selected first
```

unless a future explicitly documented policy introduces another valid ordering rule.

---

### Q3 — A Patient Cannot Be Assigned Twice

A patient may have at most one active assignment.

Two clinicians requesting the next patient simultaneously must never receive the same patient.

For example:

```text
Doctor A ─────┐
              ├── simultaneous requests
Doctor B ─────┘
```

must result in something equivalent to:

```text
Doctor A → Patient X
Doctor B → Patient Y
```

and never:

```text
Doctor A → Patient X
Doctor B → Patient X
```

---

### Q4 — Clinical Reassessment Comes From Outside the Queue Engine

The queue engine does not independently change a patient's triage category because time has elapsed.

Instead:

```text
Patient condition changes
        │
        ▼
Clinical reassessment
        │
        ▼
New triage category submitted
        │
        ▼
Queue engine updates ordering
```

The previous category and reassessment should remain auditable.

---

### Q5 — Queue Decisions Must Be Explainable

For any assignment, it should eventually be possible to determine:

* which patients were eligible at the time;
* what triage category each patient had;
* when each patient entered the queue;
* whether any reassessments had occurred;
* which patient was selected;
* when the assignment occurred; and
* which clinician or system actor initiated the assignment.

---

## Initial Patient State Model

The exact state machine is still under design, but the initial model is expected to resemble:

```text
WAITING
   │
   ▼
ASSIGNED
   │
   ▼
IN_TREATMENT
   │
   ▼
COMPLETED
```

Other terminal or exceptional states may later be added, such as:

```text
CANCELLED
LEFT
TRANSFERRED
```

A reassessment does not necessarily change the patient's workflow state. It changes the clinical triage information associated with the patient and may therefore change their position in the waiting queue.

---

## Target Design Goals

These are properties the system is intended to guarantee once implemented.

### 1. Correctness Under Concurrency

Multiple clinicians may request patients simultaneously without causing duplicate assignments or corrupting queue state.

Correctness should eventually hold beyond a single Go process so that multiple application instances can safely operate against the same persistent state.

### 2. Clinical Priority Is Preserved

The queue must consistently enforce the ordering policy derived from the externally assigned clinical triage categories.

A lower-priority eligible patient must not be assigned while a higher-priority eligible patient is waiting.

### 3. Reassessment Is Reflected Correctly

When authorized clinical staff submit a new triage category, the patient's queue position must reflect the new information.

The system must preserve enough history to distinguish the original triage assessment from later reassessments.

### 4. No Silent Data Loss

Important state changes should not be reported as successful unless the durable state required to support them has been recorded.

### 5. Auditability

It should be possible to reconstruct the relevant state and reasoning behind an assignment.

### 6. Graceful Failure Handling

Database, network, cache, notification, or application failures should be handled explicitly rather than silently ignored.

### 7. Deterministic Core Logic

Given the same patient state, eligibility rules, triage categories, arrival times, and evaluation time, the core ordering logic should produce the same result.

---

## Planned Architecture

The architecture is still being evaluated.

The current direction separates the deterministic queueing rules from persistence, HTTP, notifications, and other infrastructure.

```text
                 ┌─────────────────────┐
                 │      API Layer      │
                 └──────────┬──────────┘
                            │
                 ┌──────────▼──────────┐
                 │   Triage Engine     │
                 │                     │
                 │ ordering            │
                 │ state transitions   │
                 │ assignment rules    │
                 └──────────┬──────────┘
                            │
                  ┌─────────▼─────────┐
                  │   Persistence     │
                  │   PostgreSQL      │
                  └─────────┬─────────┘
                            │
             ┌──────────────┼──────────────┐
             │              │              │
             ▼              ▼              ▼
        Current State   Audit History   Notifications
```

The core queue rules should remain as independent as practical from:

* HTTP;
* PostgreSQL;
* authentication;
* notifications; and
* deployment infrastructure.

This should allow the ordering engine to be tested directly and deterministically.

---

## Persistence Strategy

The persistence architecture has **not yet been finalized**.

Two approaches are currently being considered.

### Option A — Relational State + Immutable Audit Log

PostgreSQL stores the current authoritative state while a separate append-only audit table records important transitions.

Conceptually:

```text
PostgreSQL
├── patients
├── assignments
├── reassessments
└── audit_events
```

This provides a comparatively simple persistence model while preserving a strong audit trail.

### Option B — Event-Sourced State

An append-only event stream acts as the source of truth and current state is reconstructed through projections.

Conceptually:

```text
Commands
   │
   ▼
Events
   │
   ├── PatientRegistered
   ├── PatientTriaged
   ├── PatientReassessed
   ├── PatientAssigned
   └── TreatmentCompleted
   │
   ▼
Current-state projections
```

Event sourcing may provide useful audit and recovery characteristics but introduces additional complexity involving replay, event versioning, projections, idempotency, and consistency.

The project will not adopt event sourcing solely because it appears architecturally sophisticated. The persistence model should be chosen based on the invariants the system actually needs to guarantee.

---

## Concurrency Model

Concurrency is a central part of the project.

An in-memory mutex may protect data inside one Go process, but it is not sufficient by itself if multiple application instances share the same database.

The eventual assignment mechanism must therefore support a scenario such as:

```text
              PostgreSQL
             /          \
            /            \
       Server A        Server B
          │               │
      Doctor A         Doctor B
```

Both servers may attempt to assign the next patient concurrently.

The system must preserve the invariant:

```text
one patient → at most one active assignment
```

The exact database transaction and locking strategy will be selected during the persistence/concurrency design phase.

---

## Queue Ordering

The initial queue ordering policy is intentionally simple.

For two eligible waiting patients `A` and `B`:

```text
1. Compare triage category.
2. Higher clinical priority wins.
3. If categories are equal, compare queue-entry time.
4. Earlier queue-entry time wins.
```

Conceptually:

```text
priority(A) < priority(B)
        │
        ├── yes → A first
        │
        └── no
             │
      same category?
             │
             └── earlier arrival first
```

The first implementation will **not** automatically modify clinical acuity based on elapsed waiting time.

Future work may investigate queue-aging or alerting policies, but those mechanisms must remain distinct from clinical reassessment unless backed by an explicitly adopted clinical policy.

---

## Tech Stack — Planned

| Layer                      | Current Direction                                            |
| -------------------------- | ------------------------------------------------------------ |
| Language                   | Go                                                           |
| Core ordering              | Go standard library / custom queue logic                     |
| Initial priority structure | `container/heap` under evaluation                            |
| API                        | REST                                                         |
| Persistence                | PostgreSQL                                                   |
| Concurrency                | Go synchronization + database-level transactional guarantees |
| Testing                    | Go `testing`                                                 |
| Race detection             | `go test -race`                                              |
| Audit storage              | To be determined                                             |
| CI                         | To be determined                                             |

`container/heap` remains a candidate for the in-memory representation, but the final data structure will depend on how reassessment, persistence, and concurrent assignment are implemented.

---

## Project Structure — Planned

```text
triage-queue/
├── cmd/
│   └── triage/
│       └── # application entrypoint
│
├── internal/
│   ├── queue/
│   │   └── # deterministic queue ordering
│   │
│   ├── domain/
│   │   └── # patients, triage categories, states, assignments
│   │
│   ├── engine/
│   │   └── # queue orchestration and state transitions
│   │
│   ├── api/
│   │   └── # HTTP handlers
│   │
│   ├── storage/
│   │   └── # PostgreSQL persistence
│   │
│   └── audit/
│       └── # audit/event history
│
├── docs/
│   ├── adr/
│   │   └── # Architecture Decision Records
│   │
│   └── TRIAGE_POLICY.md
│       └── # documented boundary between clinical input and queue policy
│
├── go.mod
└── README.md
```

No code has been written for the above structure yet.

---

# Roadmap

## Phase 0 — Define the Domain and Invariants

* [ ] Document the selected clinical triage framework
* [ ] Define supported SATS categories
* [ ] Define the clinical/software responsibility boundary
* [ ] Define patient workflow states
* [ ] Define queue eligibility
* [ ] Define the initial queue-ordering rules
* [ ] Document queue invariants
* [ ] Record important design decisions as ADRs

---

## Phase 1 — Pure Queue Engine

* [ ] Represent triage categories as domain types
* [ ] Implement clinical-priority ordering
* [ ] Implement FIFO ordering within equal categories
* [ ] Implement deterministic `NextPatient` behavior
* [ ] Keep queue logic independent of HTTP and PostgreSQL
* [ ] Unit-test ordering rules
* [ ] Test edge cases and invalid state transitions

---

## Phase 2 — Reassessment

* [ ] Support externally submitted clinical reassessments
* [ ] Reorder waiting patients after reassessment
* [ ] Preserve previous assessment information
* [ ] Test upward and downward category changes
* [ ] Ensure reassessment does not create duplicate queue entries

---

## Phase 3 — Concurrent Assignment

* [ ] Support simultaneous requests for the next patient
* [ ] Prevent duplicate patient assignment
* [ ] Define atomic assignment semantics
* [ ] Run concurrent stress tests
* [ ] Run Go race-detector tests
* [ ] Verify assignment invariants under randomized load

---

## Phase 4 — Persistence and Audit Trail

* [ ] Finalize persistence architecture
* [ ] Persist patients and queue state in PostgreSQL
* [ ] Persist assignments
* [ ] Persist reassessments
* [ ] Maintain immutable audit information
* [ ] Define transaction boundaries
* [ ] Define retry/idempotency behavior
* [ ] Verify recovery after application restart

---

## Phase 5 — Multi-Instance Concurrency

* [ ] Run multiple application instances against the same database
* [ ] Ensure two servers cannot claim the same patient
* [ ] Test database locking/claim strategy
* [ ] Simulate simultaneous assignment requests at scale

---

## Phase 6 — API Layer

* [ ] Patient intake endpoint
* [ ] Triage-category submission endpoint
* [ ] Reassessment endpoint
* [ ] Next-patient assignment endpoint
* [ ] Patient-state transition endpoints
* [ ] Administrative endpoints
* [ ] Authentication
* [ ] Authorization

---

## Phase 7 — Resilience Testing

* [ ] Simulated application crashes
* [ ] Simulated database failures
* [ ] Network interruption tests
* [ ] Retry and duplicate-request testing
* [ ] High-volume load testing
* [ ] State-recovery testing

---

## Phase 8 — Observability

* [ ] Queue-length metrics
* [ ] Waiting-time metrics
* [ ] Number of patients by triage category
* [ ] Reassessment metrics
* [ ] Assignment latency
* [ ] Failed-assignment metrics
* [ ] Structured logging
* [ ] Distributed tracing
* [ ] Alerts for abnormal conditions

---

## Possible Future Work

The following ideas are deliberately outside the first implementation:

* queue-aging policies;
* waiting-time alerts;
* configurable institutional queue policies;
* multiple treatment areas;
* specialty-specific queues;
* clinician availability;
* resource constraints;
* pre-hospital integration;
* distributed event processing;
* event-sourced persistence;
* queue-policy simulation and comparison.

Any feature that changes or interprets **clinical urgency** must remain clearly separated from ordinary queue-management logic and would require appropriate clinical validation before being treated as anything more than an engineering simulation.

---

## Running It

No runnable code exists yet.

Instructions will be added once the first implementation phase is complete.

---

## Disclaimer

This repository is an engineering project exploring concurrent, auditable, and fault-tolerant priority-queue design using emergency-department triage as the driving scenario.

It is **not a medical device**.

It does not diagnose patients, independently determine clinical urgency, or replace the judgment of trained healthcare professionals.

Any triage category used by the system is assumed to have been supplied through an appropriate clinical process.

The software described in this repository is not intended for deployment in a clinical environment.

A real-world healthcare deployment would require clinical validation, appropriate governance, security and privacy controls, regulatory review where applicable, integration with institutional workflows, and compliance with relevant healthcare and data-protection requirements.
