package user

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
	Email       string
	Role        string
	Permissions []string
	Active      bool
}

func sessionKey(sessionID string) string {
	return fmt.Sprintf("session:%s", sessionID)
}

func sessionsByEmailKey(email string) string {
	return fmt.Sprintf("sessions:%s", email)
}

func (s *Server) CreateSession(ctx context.Context, sessionID, email string, data SessionData) error {
	if s.rdb == nil {
		return errNoRedis
	}
	key := sessionKey(sessionID)
	active := "true"
	if !data.Active {
		active = "false"
	}
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, map[string]interface{}{
		"email":       email,
		"role":        data.Role,
		"permissions": strings.Join(data.Permissions, ","),
		"active":      active,
	})
	pipe.Expire(ctx, key, sessionTTL)
	pipe.SAdd(ctx, sessionsByEmailKey(email), sessionID)
	pipe.Expire(ctx, sessionsByEmailKey(email), sessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Server) GetSession(ctx context.Context, sessionID string) (*SessionData, error) {
	if s.rdb == nil {
		return nil, errNoRedis
	}
	key := sessionKey(sessionID)
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
		Email:       result["email"],
		Role:        result["role"],
		Permissions: permissions,
		Active:      result["active"] == "true",
	}, nil
}

func (s *Server) sessionIDsByEmail(ctx context.Context, email string) ([]string, error) {
	if s.rdb == nil {
		return nil, errNoRedis
	}
	ids, err := s.rdb.SMembers(ctx, sessionsByEmailKey(email)).Result()
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Server) UpdateSessionPermissions(ctx context.Context, email string, role string, permissions []string) error {
	if s.rdb == nil {
		return errNoRedis
	}
	ids, err := s.sessionIDsByEmail(ctx, email)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	pipe := s.rdb.TxPipeline()
	for _, sessionID := range ids {
		key := sessionKey(sessionID)
		exists, err := s.rdb.Exists(ctx, key).Result()
		if err != nil {
			return err
		}
		if exists == 0 {
			pipe.SRem(ctx, sessionsByEmailKey(email), sessionID)
			continue
		}
		pipe.HSet(ctx, key, map[string]interface{}{
			"role":        role,
			"permissions": strings.Join(permissions, ","),
		})
		pipe.Expire(ctx, key, sessionTTL)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Server) DeleteSession(ctx context.Context, sessionID string) error {
	if s.rdb == nil {
		return errNoRedis
	}
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}

	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, sessionKey(sessionID))
	if session != nil && session.Email != "" {
		pipe.SRem(ctx, sessionsByEmailKey(session.Email), sessionID)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Server) DeleteSessionsByEmail(ctx context.Context, email string) error {
	if s.rdb == nil {
		return errNoRedis
	}
	ids, err := s.sessionIDsByEmail(ctx, email)
	if err != nil {
		return err
	}
	pipe := s.rdb.TxPipeline()
	for _, sessionID := range ids {
		pipe.Del(ctx, sessionKey(sessionID))
	}
	pipe.Del(ctx, sessionsByEmailKey(email))
	_, err = pipe.Exec(ctx)
	return err
}
