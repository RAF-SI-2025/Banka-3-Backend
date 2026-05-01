package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const sessionTTL = 7 * 24 * time.Hour // matches refresh token lifetime

var errNoRedis = errors.New("redis client not configured")

type SessionData struct {
	Role        string
	Permissions []string
	Active      bool
}

func sessionKey(email string) string {
	return fmt.Sprintf("session:%s", email)
}

func (s *Server) CreateSession(ctx context.Context, email string, data SessionData) error {
	if s.rdb == nil {
		return errNoRedis
	}
	key := sessionKey(email)
	active := "true"
	if !data.Active {
		active = "false"
	}
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, map[string]interface{}{
		"role":        data.Role,
		"permissions": strings.Join(data.Permissions, ","),
		"active":      active,
	})
	pipe.Expire(ctx, key, sessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Server) GetSession(ctx context.Context, email string) (*SessionData, error) {
	if s.rdb == nil {
		return nil, errNoRedis
	}
	key := sessionKey(email)
	result, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, nil
	}

	var permissions []string
	if result["permissions"] != "" {
		permissions = strings.Split(result["permissions"], ",")
	} else {
		permissions = []string{}
	}

	return &SessionData{
		Role:        result["role"],
		Permissions: permissions,
		Active:      result["active"] == "true",
	}, nil
}

func (s *Server) UpdateSessionPermissions(ctx context.Context, email string, role string, permissions []string) error {
	if s.rdb == nil {
		return errNoRedis
	}
	key := sessionKey(email)
	exists, err := s.rdb.Exists(ctx, key).Result()
	if err != nil {
		return err
	}
	if exists == 0 {
		return nil // no active session to update
	}
	return s.rdb.HSet(ctx, key, map[string]interface{}{
		"role":        role,
		"permissions": strings.Join(permissions, ","),
	}).Err()
}

func (s *Server) DeleteSession(ctx context.Context, email string) error {
	if s.rdb == nil {
		return errNoRedis
	}
	return s.rdb.Del(ctx, sessionKey(email)).Err()
}
