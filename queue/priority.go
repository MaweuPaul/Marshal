package queue

// Priority is the rank of a queue entry. Lower values are more urgent.
//
// The engine does not fix how many distinct ranks exist or what range
// they span — only that ranks are ordered and comparable. Any mapping
// from human-readable priority levels (name, active flag, etc.) to a
// Priority value is a host-side concern, not something this package
// defines.
type Priority int
