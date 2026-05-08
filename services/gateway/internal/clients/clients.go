// Package clients dials the upstream gRPC services. The gateway's HTTP
// handlers consume these.
package clients

import (
	"fmt"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Set bundles every upstream client. Add new fields as services come
// online.
type Set struct {
	User userpb.UserServiceClient

	conns []*grpc.ClientConn
}

// Dial connects to all upstream services. Caller defers Close().
func Dial(addrs Addrs) (*Set, error) {
	s := &Set{}
	userConn, err := dial(addrs.User)
	if err != nil {
		return nil, fmt.Errorf("dial user: %w", err)
	}
	s.conns = append(s.conns, userConn)
	s.User = userpb.NewUserServiceClient(userConn)
	return s, nil
}

// Close releases every dialed connection.
func (s *Set) Close() {
	for _, c := range s.conns {
		_ = c.Close()
	}
}

// Addrs holds the gRPC dial targets for each upstream service.
type Addrs struct {
	User         string
	Bank         string
	Trading      string
	Exchange     string
	Notification string
}

func dial(target string) (*grpc.ClientConn, error) {
	if target == "" {
		return nil, fmt.Errorf("empty target")
	}
	return grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}
