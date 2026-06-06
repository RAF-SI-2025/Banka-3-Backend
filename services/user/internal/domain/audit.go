package domain

import "time"

// AuditEntry is one recorded administrative action (todoSpec "Audit log").
type AuditEntry struct {
	ID          string
	Action      string
	ActorID     string
	ActorKind   string
	ActorName   string
	TargetID    string
	TargetLabel string
	OldValue    string
	NewValue    string
	Note        string
	CreatedAt   time.Time
}

// AuditFilter narrows an audit-log listing. Zero-value fields are
// ignored, so an empty filter returns everything.
type AuditFilter struct {
	// Action, when set, matches the action key exactly.
	Action string
	// Actor, when set, matches actor_id exactly OR actor_name
	// (case-insensitive substring).
	Actor string
	// From / To are inclusive timestamp bounds; nil means unbounded.
	From *time.Time
	To   *time.Time
}
