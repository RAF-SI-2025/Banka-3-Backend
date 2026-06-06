// Package domain holds the notification service's entities. No I/O.
package domain

import "time"

// Notification is one in-app notification row. ReadAt is nil while the
// notification is unread.
type Notification struct {
	ID        string
	UserID    string
	UserKind  string
	Kind      string
	Title     string
	Body      string
	ReadAt    *time.Time
	CreatedAt time.Time
}

// Read reports whether the notification has been seen.
func (n *Notification) Read() bool { return n.ReadAt != nil }
