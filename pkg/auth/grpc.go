package auth

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Metadata keys propagated by the gateway. Lowercase per gRPC convention.
const (
	MDUserID         = "x-user-id"
	MDUserKind       = "x-user-kind"
	MDPermissions    = "x-permissions"
	MDSessionVersion = "x-session-version"
)

// Origin-principal metadata keys. Set by service-to-service callers
// (notably trading→bank) alongside the authz principal so the bank
// service can audit the real client/actuary who initiated the call,
// not the sentinel that satisfies the bank's permission check.
// See [[reference_be16_sentinel_origin_forwarding]] memory.
const (
	MDOriginUserID   = "x-origin-user-id"
	MDOriginUserKind = "x-origin-user-kind"
)

// AttachToOutgoing copies p into ctx as outgoing gRPC metadata. Used by
// the gateway when calling internal services on behalf of an
// authenticated user.
func AttachToOutgoing(ctx context.Context, p Principal) context.Context {
	md := metadata.Pairs(
		MDUserID, p.UserID,
		MDUserKind, string(p.UserKind),
		MDPermissions, strings.Join(p.Permissions, ","),
		MDSessionVersion, strconv.FormatInt(p.SessionVersion, 10),
	)
	return metadata.NewOutgoingContext(ctx, md)
}

// AttachWithOriginToOutgoing attaches p as the outgoing authz principal
// (same as AttachToOutgoing) plus the separate origin headers. Used by
// trading's bank-client adapter to forward the real initiator while
// still satisfying the bank's authz check with a sentinel/admin.
// Origin lives in distinct headers so the bank's existing
// MetadataInterceptor keeps consuming the authz pair unchanged.
func AttachWithOriginToOutgoing(ctx context.Context, p Principal, origin Principal) context.Context {
	md := metadata.Pairs(
		MDUserID, p.UserID,
		MDUserKind, string(p.UserKind),
		MDPermissions, strings.Join(p.Permissions, ","),
		MDSessionVersion, strconv.FormatInt(p.SessionVersion, 10),
		MDOriginUserID, origin.UserID,
		MDOriginUserKind, string(origin.UserKind),
	)
	return metadata.NewOutgoingContext(ctx, md)
}

// MetadataInterceptor reconstructs a Principal from incoming gRPC
// metadata if present, and stores it on ctx via WithPrincipal. Handlers
// that need a principal call PrincipalFrom; those that don't need one
// (Login, public RPCs) ignore it. Also extracts the origin-principal
// pair (set by trading→bank cross-service calls) into ctx via
// WithOrigin so the bank's audit layer can record the real initiator
// instead of the trading-side sentinel.
func MetadataInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return handler(ctx, req)
		}
		// Origin-principal pair (optional, set by service-to-service
		// callers). Attach independently of the authz principal so a
		// future cross-cutting use (logs, c5 inter-bank handoff)
		// always sees the real initiator.
		if origID := first(md, MDOriginUserID); origID != "" {
			ctx = WithOrigin(ctx, Principal{
				UserID:   origID,
				UserKind: UserKind(first(md, MDOriginUserKind)),
			})
		}
		userID := first(md, MDUserID)
		if userID == "" {
			return handler(ctx, req)
		}
		var perms []string
		if raw := first(md, MDPermissions); raw != "" {
			perms = strings.Split(raw, ",")
		}
		var sv int64
		if raw := first(md, MDSessionVersion); raw != "" {
			sv, _ = strconv.ParseInt(raw, 10, 64)
		}
		p := Principal{
			UserID:         userID,
			UserKind:       UserKind(first(md, MDUserKind)),
			Permissions:    perms,
			SessionVersion: sv,
		}
		return handler(WithPrincipal(ctx, p), req)
	}
}

func first(md metadata.MD, key string) string {
	v := md.Get(key)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
