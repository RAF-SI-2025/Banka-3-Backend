package server

import (
	"context"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// =====================================================================
// Verification history — internal RPCs (spec p.84)
// =====================================================================

func (s *Server) RecordVerificationEvent(ctx context.Context, in *userpb.RecordVerificationEventRequest) (*emptypb.Empty, error) {
	if err := s.Svc.RecordVerificationEvent(ctx, in.GetId(), in.GetUserId(), in.GetActionKind()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) ResolveVerificationEvent(ctx context.Context, in *userpb.ResolveVerificationEventRequest) (*emptypb.Empty, error) {
	if err := s.Svc.ResolveVerificationEvent(ctx, in.GetId(), in.GetSuccess()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) ListVerificationHistory(ctx context.Context, in *userpb.ListVerificationHistoryRequest) (*userpb.ListVerificationHistoryResponse, error) {
	events, err := s.Svc.ListVerificationHistory(ctx, in.GetUserId(), int(in.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := make([]*userpb.VerificationEvent, 0, len(events))
	for i := range events {
		out = append(out, verificationEventToProto(events[i]))
	}
	return &userpb.ListVerificationHistoryResponse{Events: out}, nil
}

func verificationEventToProto(e domain.VerificationEvent) *userpb.VerificationEvent {
	pe := &userpb.VerificationEvent{
		Id:         e.ID,
		ActionKind: e.ActionKind,
		Status:     e.Status,
		CreatedAt:  timestamppb.New(e.CreatedAt),
	}
	if e.ResolvedAt != nil {
		pe.ResolvedAt = timestamppb.New(*e.ResolvedAt)
	}
	return pe
}
