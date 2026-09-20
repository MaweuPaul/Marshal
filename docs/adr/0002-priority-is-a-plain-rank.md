# 2. Priority is a plain, ordered rank — not a leveled type

## Status

Accepted

## Context

Priority-based systems often model priority as a small set of named levels
(e.g. Red/Orange/Yellow/Green, or Critical/High/Medium/Low). It's tempting
to bake that shape into the engine itself — an enum, a bounded range, or a
`PriorityLevel` struct with a name and metadata — since that's how most
reference domains (including this project's own emergency-department
triage example) actually present priority to a human.

Doing that would mean the queue package owns: how many levels exist, what
they're called, and what range is valid. That directly conflicts with the
project's core boundary: the engine enforces ordering, the host defines
what's being ordered.

## Decision

`Priority` is a plain `int`-based type (see `queue/priority.go`). It is:

- **Ordered and comparable** — the only property the engine relies on.
- **Not bounded** — no fixed range, no fixed count of distinct values.
- **Not paired with a name, label, or level concept** in this package —
  a human-readable `PriorityLevel` (name + rank), if a host wants one, is
  entirely a host-side or reference-layer concern (see the
  `examples/emergency-department` SATS mapping for what that looks like).

## Consequences

- Any host can configure its own scheme — 4 levels, 7 levels, a sparse
  range like `100/250/500/999` — without any change to `queue`. This is
  exercised directly by `TestQueueIsAgnosticToPriorityScheme`.
- The engine cannot validate that a given `Priority` value is "meaningful"
  (e.g. reject an out-of-range value) — that validation, if wanted, belongs
  to whatever host-side layer produces the rank in the first place.
- Two hosts using different ranges can't be mixed within a single `Queue`
  instance in a way that makes sense (rank `1` from one scheme and rank
  `1` from another scheme are not comparable in any meaningful domain
  sense, even though the engine will happily order them together). This is
  considered an acceptable limitation: a single `Queue` is expected to
  serve one coherent priority scheme, chosen and owned by its host.
