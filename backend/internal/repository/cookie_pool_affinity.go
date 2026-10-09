package repository

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var _ service.CookiePoolAffinityCache = (*gatewayCache)(nil)

func cookiePoolAffinityKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("cookie_pool_affinity:%d:%s", groupID, sessionHash)
}

// Use Redis time for consistent expiry across instances. Atomic updates retain
// at most three distinct accounts used within ten minutes, even on parallel turns.
var rememberCookiePoolAffinityScript = redis.NewScript(`
local id = ARGV[1]
if id == '0' then id = redis.call('GET', KEYS[2]) end
if not id or id == '0' then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) + tonumber(t[2]) / 1000000
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - 600)
redis.call('ZADD', KEYS[1], now, id)
redis.call('ZREMRANGEBYRANK', KEYS[1], 0, -4)
redis.call('EXPIRE', KEYS[1], 600)
return 1
`)

var readCookiePoolAffinityScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) + tonumber(t[2]) / 1000000
return redis.call('ZREVRANGEBYSCORE', KEYS[1], '+inf', '(' .. tostring(now - 600), 'LIMIT', 0, 3)
`)

// accountID=0 refreshes the current primary account without a separate GET.
func (c *gatewayCache) RememberCookiePoolAffinity(ctx context.Context, groupID int64, sessionHash string, accountID int64) error {
	return rememberCookiePoolAffinityScript.Run(ctx, c.rdb, []string{cookiePoolAffinityKey(groupID, sessionHash), buildSessionKey(groupID, sessionHash)}, accountID).Err()
}

func (c *gatewayCache) ReadCookiePoolAffinity(ctx context.Context, groupID int64, sessionHash string) ([]int64, error) {
	values, err := readCookiePoolAffinityScript.Run(ctx, c.rdb, []string{cookiePoolAffinityKey(groupID, sessionHash)}).StringSlice()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
