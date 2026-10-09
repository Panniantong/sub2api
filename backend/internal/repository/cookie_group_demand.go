package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// One shared Redis clock; request leases expire after crashes. Keep one maximum
// per second (at most 3601 samples) so short bursts survive the worker's 1s scan.
var cookieGroupDemandScript = redis.NewScript(`
local now = tonumber(redis.call('TIME')[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-60)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now-3600)
if ARGV[1] == 'touch' then redis.call('ZADD', KEYS[1], now, ARGV[2]) end
local current = redis.call('ZCARD', KEYS[1])
local bucket = tostring(now)
local stored = tonumber(redis.call('HGET', KEYS[3], bucket) or '0')
redis.call('ZADD', KEYS[2], now, bucket)
redis.call('HSET', KEYS[3], bucket, math.max(stored,current))
-- Prune hash buckets alongside the timestamp index.
local last = tonumber(redis.call('GET', KEYS[4]) or tostring(now-3601))
for t=math.max(last,now-7200),now-3600 do redis.call('HDEL',KEYS[3],tostring(t)) end
redis.call('SET',KEYS[4],now-3599,'EX',3660)
redis.call('EXPIRE',KEYS[1],60)
redis.call('EXPIRE',KEYS[2],3660)
redis.call('EXPIRE',KEYS[3],3660)
local peak = current
if ARGV[1] == 'read' then
  local buckets = redis.call('ZRANGEBYSCORE',KEYS[2],now-tonumber(ARGV[2])+1,'+inf')
  for _,t in ipairs(buckets) do peak=math.max(peak,tonumber(redis.call('HGET',KEYS[3],t) or '0')) end
end
return {current,peak}
`)

func cookieGroupDemandKeys(id int64) []string {
	key := fmt.Sprintf("cookie:demand:{%d}", id)
	return []string{key + ":requests", key + ":times", key + ":peaks", key + ":pruned"}
}
func (c *concurrencyCache) TouchCookieGroupDemand(ctx context.Context, id int64, request string) error {
	return cookieGroupDemandScript.Run(ctx, c.rdb, cookieGroupDemandKeys(id), "touch", request).Err()
}
func (c *concurrencyCache) ReleaseCookieGroupDemand(ctx context.Context, id int64, request string) error {
	return c.rdb.ZRem(ctx, cookieGroupDemandKeys(id)[0], request).Err()
}
func (c *concurrencyCache) ReadCookieGroupDemand(ctx context.Context, id int64, seconds int) (service.CookieGroupDemand, error) {
	values, err := cookieGroupDemandScript.Run(ctx, c.rdb, cookieGroupDemandKeys(id), "read", seconds).Int64Slice()
	if err != nil {
		return service.CookieGroupDemand{}, err
	}
	return service.CookieGroupDemand{Current: int(values[0]), Peak: int(values[1])}, nil
}
