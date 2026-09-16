package repository

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// securityPolicySessionStore 是 SecurityPolicySessionStore 的 Redis 实现：
// HASH scope -> field(=会话 key hash)，TTL 随每次命中刷新。管理端按 scope
// 整体解封（DEL），无需扫描。
type securityPolicySessionStore struct {
	rdb *redis.Client
}

func NewSecurityPolicySessionStore(rdb *redis.Client) service.SecurityPolicySessionStore {
	return &securityPolicySessionStore{rdb: rdb}
}

func (s *securityPolicySessionStore) MarkSessionBlocked(ctx context.Context, scope string, fields []string, ttl time.Duration) error {
	scope = strings.TrimSpace(scope)
	fields = trimSecurityPolicySessionFields(fields)
	if s == nil || s.rdb == nil || scope == "" || len(fields) == 0 {
		return nil
	}
	members := make(map[string]any, len(fields))
	for _, field := range fields {
		members[field] = 1
	}
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, scope, members)
	if ttl > 0 {
		pipe.Expire(ctx, scope, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *securityPolicySessionStore) FindBlockedSessionField(ctx context.Context, scope string, fields []string) (string, error) {
	scope = strings.TrimSpace(scope)
	fields = trimSecurityPolicySessionFields(fields)
	if s == nil || s.rdb == nil || scope == "" || len(fields) == 0 {
		return "", nil
	}
	for _, field := range fields {
		exists, err := s.rdb.HExists(ctx, scope, field).Result()
		if err != nil {
			return "", err
		}
		if exists {
			return field, nil
		}
	}
	return "", nil
}

func (s *securityPolicySessionStore) UnblockSessionScope(ctx context.Context, scope string) (bool, error) {
	scope = strings.TrimSpace(scope)
	if s == nil || s.rdb == nil || scope == "" {
		return false, nil
	}
	deleted, err := s.rdb.Del(ctx, scope).Result()
	if err != nil {
		return false, err
	}
	return deleted > 0, nil
}

func trimSecurityPolicySessionFields(fields []string) []string {
	out := fields[:0]
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}
