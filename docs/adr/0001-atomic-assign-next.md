# 1. AssignNext is a single atomic operation, not peek-then-assign

## Status

Accepted

## Context

A queue engine that hands out work to concurrent callers needs an API for
"give me the next thing to do." The obvious two-step shape is:

```go
entry := queue.Next()
queue.Assign(entry, assigneeID)
```

This is easy to read, but it has a race window: between `Next()` returning
an entry and `Assign()` claiming it, another caller could call `Next()` and
get the same entry. Closing that window from the outside would require the
caller to hold a lock across both calls — which defeats the point of the
queue managing its own concurrency, and is easy to get wrong (forget to
unlock, unlock in the wrong order, etc.).

## Decision

`AssignNext(assigneeID)` is a single method that selects the highest-ranked
eligible entry, removes it from the active waiting queue, and creates its
immutable assignment fact, all while holding the queue's internal lock. No
caller-visible "peek" step exists that returns an entry without claiming it
(`Peek` exists separately, but it is documented as inspection-only, not a
step in an assign workflow — see its doc comment in `queue/queue.go`).

## Consequences

- The invariant "two concurrent callers never receive the same entry" (Q3
  in the README) is enforced entirely inside the package; callers cannot
  violate it by misusing the API, because there is no way to select an
  entry without claiming it in the same call.
- The API surface is smaller: one method instead of two, and no separate
  "claim" step for callers to remember.
- The tradeoff is that `AssignNext` does two logically distinct things
  (select + claim) in one call. This is considered acceptable because the
  two are inseparable under concurrency — splitting them apart is what
  creates the race in the first place.
