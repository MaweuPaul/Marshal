# Triage Queue

A priority queueing engine for emergency-room patient triage, built in
Go. It aims to answer one question correctly, even under concurrent
load: **given everyone currently waiting, who should be seen next?**

> **Status:** Planning stage. Nothing in this repository has been
> implemented yet. Everything below describes the intended design, not
> existing functionality. See [Disclaimer](#disclaimer).

## Problem Statement

Emergency rooms do not treat patients first-come-first-served. Patients
are assigned an acuity/severity level (e.g. ESI 1–5, 1 being immediately
life-threatening) and must be seen in an order that reflects both:

- **Medical urgency** — more severe patients are seen first.
- **Time-in-queue** — a moderately urgent patient who has waited too
  long may need to become a higher priority, since prolonged waiting is
  itself a risk.

The goal of this project is a system that can hold that ordering
correctly under real operational conditions: multiple doctors pulling
from the same queue at once, new patients arriving mid-shift, severity
being re-assessed after arrival, and no loss of state on a crash.

## Target Design Goals

These are the properties the system is intended to guarantee once
built. None of them are implemented yet:

1. **Correctness under concurrency** — two doctors should never be
   assigned the same patient, even under simultaneous requests.
2. **No silent data loss** — state-changing events should be persisted
   before being considered complete.
3. **Priority reflects reality** — a patient's position in the queue
   should update as time passes and as new information arrives.
4. **Auditability** — it should be possible to reconstruct why the
   system made a given call.
5. **Graceful degradation** — a downstream failure (DB, cache) should
   be handled explicitly, not silently swallowed.

## Planned Architecture
                 ┌─────────────────────┐
                 │      API Layer         │
                 └──────────┬───────────┘
                            │
                 ┌──────────▼───────────┐
                 │   Triage Engine         │
                 │  (priority queue +      │
                 │   re-prioritization)    │
                 └──────────┬───────────┘
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
┌─────────▼───────┐ ┌───────▼──────┐ ┌────────▼────────┐
│   Event Log        │ │   Read Store    │ │   Notifications    │
└───────────────────┘ └────────────────┘ └────────────────────┘

The intent is an event-sourced design — an append-only log as the
source of truth, with current state computed as a projection — for
auditability and crash recovery. This is a design goal only.

## Tech Stack (Planned)

| Layer              | Choice                        |
|---------------------|--------------------------------|
| Language            | Go                              |
| Core queue          | `container/heap`                |
| API                 | REST                            |
| Persistence         | PostgreSQL                      |
| Concurrency control | Mutex/channels                  |
| Testing             | Go `testing` + race detector    |

## Project Structure (Planned)
triage-queue/
├── cmd/
│ └── triage/ # Entrypoint (not yet created)
├── internal/
│ ├── queue/ # Core priority queue
│ ├── eventlog/ # Append-only event store
│ ├── engine/ # Orchestrates queue + eventlog + rules
│ ├── api/ # HTTP handlers
│ └── storage/ # Postgres persistence layer
├── docs/
│ └── adr/ # Architecture Decision Records
├── go.mod
└── README.md


No code has been written for any of the above yet.

## Roadmap

### Phase 1 — Core Queue
- [ ] Severity-ordered priority queue (min-heap)
- [ ] Tie-breaking by arrival time
- [ ] Unit tests for ordering logic

### Phase 2 — Time-Based Escalation
- [ ] Priority increases the longer a patient waits
- [ ] Queue re-sorts based on elapsed time, not just on insert

### Phase 3 — Concurrency Safety
- [ ] Multiple doctors can pull from the queue simultaneously
- [ ] No two doctors are ever assigned the same patient
- [ ] Race-condition tests (`-race`) as part of CI

### Phase 4 — Persistence & Audit Trail
- [ ] Event-sourced storage (append-only log)
- [ ] Crash recovery — state is rebuilt from the event log
- [ ] Ability to reconstruct any past decision

### Phase 5 — API Layer
- [ ] REST endpoints for intake, assignment, and admin actions
- [ ] Authentication/authorization

### Phase 6 — Resilience Testing
- [ ] Simulated crashes and network partitions
- [ ] Load testing under high patient volume

### Phase 7 — Observability
- [ ] Metrics (queue length, wait times, escalation counts)
- [ ] Tracing and alerting on anomalies

## Running It

No runnable code exists yet. Instructions will be added once Phase 1
is implemented.

## Disclaimer

This is an engineering project exploring correct, concurrent,
auditable priority-queue design using a triage scenario as the driving
example. It is not a certified medical device and is not intended for
deployment in a clinical environment. A real hospital deployment would
require regulatory clearance, clinical validation, and data-protection
compliance well beyond the scope of this repo.
