package server

import (
	"context"
	"time"

	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/notification/internal/domain"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// CreateNotification persists one in-app notification. Internal RPC
// (no gateway route): the user_id/user_kind come from the trusted
// caller, not an end user. Returns the stored row.
func (s *Server) CreateNotification(ctx context.Context, in *notifpb.CreateNotificationRequest) (*notifpb.Notification, error) {
	if s.Store == nil {
		return nil, apperr.Internal("notification store not wired", nil)
	}
	kind := in.GetKind()
	if kind == "" {
		kind = "generic"
	}
	n, err := s.Store.Insert(ctx, &domain.Notification{
		UserID:   in.GetUserId(),
		UserKind: in.GetUserKind(),
		Kind:     kind,
		Title:    in.GetTitle(),
		Body:     in.GetBody(),
	})
	if err != nil {
		return nil, err
	}
	s.Log.InfoContext(ctx, "in-app notification created",
		"user_id", n.UserID, "kind", n.Kind)
	return toProto(n), nil
}

// ListNotifications returns the caller's own feed, newest first. The
// recipient is the authenticated principal — never a request field —
// so a user can only read their own notifications.
func (s *Server) ListNotifications(ctx context.Context, in *notifpb.ListNotificationsRequest) (*notifpb.ListNotificationsResponse, error) {
	if s.Store == nil {
		return nil, apperr.Internal("notification store not wired", nil)
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.UserID == "" {
		return nil, apperr.Unauthenticated("no principal")
	}

	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	offset := (page - 1) * pageSize

	items, err := s.Store.ListByUser(ctx, p.UserID, in.GetUnreadOnly(), pageSize, offset)
	if err != nil {
		return nil, err
	}
	total, err := s.Store.CountByUser(ctx, p.UserID, in.GetUnreadOnly())
	if err != nil {
		return nil, err
	}
	unread, err := s.Store.CountUnread(ctx, p.UserID)
	if err != nil {
		return nil, err
	}

	out := make([]*notifpb.Notification, 0, len(items))
	for _, n := range items {
		out = append(out, toProto(n))
	}
	return &notifpb.ListNotificationsResponse{
		Items:    out,
		Page:     int32(page),
		PageSize: int32(pageSize),
		Total:    total,
		Unread:   unread,
	}, nil
}

// MarkNotificationRead flips one of the caller's notifications to read.
// Scoped to the principal: another user's id resolves to NotFound.
func (s *Server) MarkNotificationRead(ctx context.Context, in *notifpb.MarkNotificationReadRequest) (*notifpb.Notification, error) {
	if s.Store == nil {
		return nil, apperr.Internal("notification store not wired", nil)
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.UserID == "" {
		return nil, apperr.Unauthenticated("no principal")
	}
	n, err := s.Store.MarkRead(ctx, p.UserID, in.GetId())
	if err != nil {
		return nil, err
	}
	return toProto(n), nil
}

// MarkAllNotificationsRead flips every unread notification of the caller
// to read.
func (s *Server) MarkAllNotificationsRead(ctx context.Context, _ *notifpb.MarkAllNotificationsReadRequest) (*notifpb.MarkAllNotificationsReadResponse, error) {
	if s.Store == nil {
		return nil, apperr.Internal("notification store not wired", nil)
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.UserID == "" {
		return nil, apperr.Unauthenticated("no principal")
	}
	marked, err := s.Store.MarkAllRead(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return &notifpb.MarkAllNotificationsReadResponse{Marked: marked}, nil
}

func toProto(n *domain.Notification) *notifpb.Notification {
	out := &notifpb.Notification{
		Id:        n.ID,
		UserId:    n.UserID,
		UserKind:  n.UserKind,
		Kind:      n.Kind,
		Title:     n.Title,
		Body:      n.Body,
		Read:      n.Read(),
		CreatedAt: n.CreatedAt.Format(time.RFC3339),
	}
	if n.ReadAt != nil {
		out.ReadAt = n.ReadAt.Format(time.RFC3339)
	}
	return out
}
