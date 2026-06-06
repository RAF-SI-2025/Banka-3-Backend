package server

import (
	"context"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RecordAuditEntry is the internal cross-service write path.
func (s *Server) RecordAuditEntry(ctx context.Context, in *userpb.RecordAuditEntryRequest) (*emptypb.Empty, error) {
	if err := s.Svc.RecordAuditEntry(ctx, domain.AuditEntry{
		Action:      in.GetAction(),
		ActorID:     in.GetActorId(),
		ActorKind:   in.GetActorKind(),
		TargetID:    in.GetTargetId(),
		TargetLabel: in.GetTargetLabel(),
		OldValue:    in.GetOldValue(),
		NewValue:    in.GetNewValue(),
		Note:        in.GetNote(),
	}); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ListAuditLog returns audit entries (admin/supervisor only).
func (s *Server) ListAuditLog(ctx context.Context, in *userpb.ListAuditLogRequest) (*userpb.ListAuditLogResponse, error) {
	f := domain.AuditFilter{Action: in.GetAction(), Actor: in.GetActor()}
	if v := in.GetFrom(); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, apperr.Validation("from: invalid RFC3339 timestamp")
		}
		f.From = &t
	}
	if v := in.GetTo(); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, apperr.Validation("to: invalid RFC3339 timestamp")
		}
		f.To = &t
	}

	page, pageSize := int(in.GetPage()), int(in.GetPageSize())
	items, total, err := s.Svc.ListAuditLog(ctx, f, page, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]*userpb.AuditEntry, 0, len(items))
	for _, e := range items {
		out = append(out, auditToProto(e))
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	return &userpb.ListAuditLogResponse{
		Items:    out,
		Page:     int32(page),
		PageSize: int32(pageSize),
		Total:    total,
	}, nil
}

func auditToProto(e *domain.AuditEntry) *userpb.AuditEntry {
	return &userpb.AuditEntry{
		Id:          e.ID,
		Action:      e.Action,
		ActorId:     e.ActorID,
		ActorKind:   e.ActorKind,
		ActorName:   e.ActorName,
		TargetId:    e.TargetID,
		TargetLabel: e.TargetLabel,
		OldValue:    e.OldValue,
		NewValue:    e.NewValue,
		Note:        e.Note,
		CreatedAt:   timestamppb.New(e.CreatedAt),
	}
}
