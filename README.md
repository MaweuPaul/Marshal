
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
