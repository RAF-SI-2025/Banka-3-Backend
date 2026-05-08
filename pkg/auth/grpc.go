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

// MetadataInterceptor reconstructs a Principal from incoming gRPC
// metadata if present, and stores it on ctx via WithPrincipal. Handlers
// that need a principal call PrincipalFrom; those that don't need one
// (Login, public RPCs) ignore it.
func MetadataInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return handler(ctx, req, info)
		}
		userID := first(md, MDUserID)
		if userID == "" {
			return handler(ctx, req, info)
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
		return handler(WithPrincipal(ctx, p), req, info)
	}
}

func first(md metadata.MD, key string) string {
	v := md.Get(key)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
