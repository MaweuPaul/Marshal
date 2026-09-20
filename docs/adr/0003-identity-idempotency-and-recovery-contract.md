# 3. Identity, idempotency, and the recovery contract (v1 boundaries)

## Status

Accepted

## Context

Before adding more commands, several questions needed explicit answers instead
of implicit assumptions:

1. What uniquely identifies a command, an assignment, and an event?
2. Can commands safely be retried?
3. Is `AssignNext` idempotent for a caller-supplied command ID?
4. What happens after a process restart?
5. How does the host know whether an assignment was committed before a crash?
6. What ordering guarantees exist for emitted events?
7. Can events be applied twice?
8. What state is authoritative: engine memory or external persistence?

Auditing the code as it stood answered some of these by default ("no
guarantee exists") rather than by design, which is a real risk: a host that
assumes retry-safety where none exists can silently double-book or misroute
an entry.

## Decision

### 1. Identity

- A **command** is now identified by a caller-supplied `commandID string`,
  required on `AssignNext` specifically (see below). Other commands remain
  unkeyed in v1 — see "Consequences" for why that's an accepted gap, not an
  oversight.
- An **assignment** is identified by `EntryID` while active (Q3 guarantees at
  most one), and by `(EntryID, Sequence, AssignedAt)` historically — there is
  no separate `AssignmentID` in v1; nothing currently needs to distinguish
  two past assignments of the same entry from each other more precisely than
  that.
- An **event** now carries `EventSeq uint64` — a single counter, incremented
  under the same lock that already serializes every command, assigned to
  every event the engine emits regardless of type.

### 2 & 3. Retry safety and AssignNext idempotency

Auditing each command:

- `UpdatePriority`, `Reassign` retried after success: safe in effect (same
  end state), but each retry emits a redundant event. Acceptable for v1 —
  redundant events are a host-side dedup problem if it matters to them, not a
  correctness problem for the queue itself.
- `RemoveEntry`, `CancelAssignment` retried after success: return a
  distinguishable error (`ErrEntryNotFound` / `ErrEntryNotAssigned`) a caller
  can treat as "already done." Acceptable for v1 under the same reasoning.
- `AssignNext` retried: **not safe**. A retry doesn't repeat the previous
  effect — it claims a *different* entry. This is a correctness bug, not a
  minor inefficiency, and it's the one command where "just call it again"
  actively causes double-booking risk from the host's perspective (two
  attempts, two different patients claimed, host only expected one).

`AssignNext` therefore now requires a non-empty `commandID string` parameter.
If the same `commandID` is passed again, the engine returns the *original*
`Assignment` and `AssignmentCreated` instead of claiming a new entry. An
empty `commandID` is rejected with `ErrEmptyCommandID` — there is no
"opt out of safety" path, because the failure mode (silently claiming the
wrong entry) is too easy to hit by accident otherwise.

The command-result cache is bounded (a fixed-size FIFO eviction, not
unbounded), trading perfect long-term dedup for bounded memory — this is an
"idempotency window" (recent retries are deduplicated; a retry arriving long
after the cache has evicted that ID is treated as new). This matches how
most real-world idempotency-key systems behave in practice.

### 4 & 5. Restart and crash recovery

Unchanged from the existing "Persistence Belongs to the Host" design: engine
memory is not durable, and this repository does not add persistence to
change that. The `commandID` cache above lives in process memory too — it
protects against retries *within a process's lifetime*, not across a crash
and restart.

Across a restart, correctness is entirely the host's responsibility: the
host must persist `AssignmentCreated` (and other events) *before*
considering an assignment committed, and must replay its own stored state on
restart. This repository does not yet provide an `Apply(event)` replay
mechanism — it remains explicitly future work (see the README's "Event
replay" section) — but `EventSeq` is added now specifically so that future
replay logic has a monotonic ordinal to key off of without a breaking change
later.

### 6. Event ordering

`EventSeq` is strictly increasing and assigned under the same mutex that
already serializes every command, so it is a true total order for events
across the whole queue — not just within one entry's history (`Sequence` is
arrival order for entries; `EventSeq` is emission order for events, and they
are deliberately different counters for different purposes).

### 7. Double-application of events

Not yet applicable — there is no `Apply(event)` method in v1. When one is
eventually built, `EventSeq` gives it a natural dedup key: skip any event
whose `EventSeq` is not greater than the queue's last-applied `EventSeq`.

### 8. Authoritative state

Engine memory is authoritative for **live, in-process decisions** — it's the
only thing enforcing Q1–Q6 in real time, and nothing overrides it mid-flight.
The host's persisted event log is authoritative for **recovery and audit** —
once the process holding that memory is gone, the log is the only thing
left. These are not competing sources of truth; they are authoritative for
different questions.

## Consequences

- `AssignNext`'s signature changes to `AssignNext(assigneeID AssigneeID,
  commandID string) (Assignment, AssignmentCreated, error)` — a breaking
  change to the v1 API, made now (before this project has external
  consumers) rather than later.
- Every event struct gains an `EventSeq uint64` field — additive, not
  breaking, since Go's keyed struct literals mean existing construction
  sites don't need to change.
- The engine now holds a small amount of retry-history state
  (`commandResults`), bounded in size, that didn't exist before. This is a
  deliberate, scoped exception to "the engine only holds queue state" — it
  exists purely to make one specific command retry-safe, not as a general
  request cache.
- Commands other than `AssignNext` remain unkeyed in v1. If a host later
  needs the same retry-safety for, say, `UpdatePriority`, the same pattern
  (a required `commandID`, a bounded result cache) should be applied there
  too — but it is not added preemptively, per this project's "don't
  over-design up front" stance.
