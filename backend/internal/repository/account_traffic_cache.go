package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

type accountTrafficCache struct{ rdb *redis.Client }

func NewAccountTrafficCache(rdb *redis.Client) service.AccountTrafficCache {
	return &accountTrafficCache{rdb: rdb}
}
func trafficKeys(id int64) []string {
	base := fmt.Sprintf("account_traffic:{%d}:", id)
	return []string{base + "state", base + "leases", base + "minute"}
}

// A single Lua transaction uses Redis time for the sliding 60-second ceiling,
// token-bucket burst allowance and adaptive in-flight admission. Every key for
// one account carries the same hash tag, including on Redis Cluster.
var trafficAcquireScript = redis.NewScript(`
redis.replicate_commands()
local t=redis.call('TIME'); local now=tonumber(t[1])*1000+tonumber(t[2])/1000
local rev=tonumber(ARGV[1]);local sig=ARGV[2];local hard=tonumber(ARGV[3])
local rpmOn=ARGV[4]=='1';local rpm=tonumber(ARGV[5]);local burst=tonumber(ARGV[6])
local adaptive=ARGV[7]=='1';local automatic=ARGV[8]=='automatic';local minimum=math.min(tonumber(ARGV[9]),math.max(1,hard))
local oldrev=tonumber(redis.call('HGET',KEYS[1],'revision') or '0')
local oldsig=redis.call('HGET',KEYS[1],'signature')
if rev<oldrev and oldsig~=sig then
 if not rpmOn and not (adaptive and automatic) and redis.call('HGET',KEYS[1],'enforces')=='0' then
  return {1,0,4,hard,hard,0,0}
 end
 return {0,1000,3,hard,hard,0,0}
end
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',now-120000)
redis.call('ZREMRANGEBYSCORE',KEYS[3],'-inf',now-60000)
local count=redis.call('ZCARD',KEYS[2]);local minute=redis.call('ZCARD',KEYS[3])
local recommended=tonumber(redis.call('HGET',KEYS[1],'recommended') or tostring(hard))
if hard>0 then recommended=math.max(minimum,math.min(hard,recommended)) else recommended=0 end
local effective=hard;if adaptive and automatic then effective=recommended end
local tokens=tonumber(redis.call('HGET',KEYS[1],'tokens') or tostring(burst))
local last=tonumber(redis.call('HGET',KEYS[1],'refilled_at') or tostring(now))
tokens=math.min(burst,tokens+math.max(0,now-last)*rpm/60000)
redis.call('HSET',KEYS[1],'revision',math.max(rev,oldrev),'signature',sig,'recommended',recommended,'tokens',tokens,'refilled_at',now)
redis.call('HSET',KEYS[1],'enforces',(rpmOn or (adaptive and automatic)) and '1' or '0')
redis.call('HSETNX',KEYS[1],'observed_since',now)
redis.call('EXPIRE',KEYS[1],86400)
if adaptive and automatic and count>=effective then
 redis.call('HINCRBY',KEYS[1],'rejected_concurrency',1)
 return {0,1000,2,effective,recommended,count,minute}
end
if rpmOn then
 local wait=0
 if minute>=rpm then local boundary=redis.call('ZRANGE',KEYS[3],minute-rpm,minute-rpm,'WITHSCORES');wait=math.max(wait,tonumber(boundary[2])+60000-now) end
 if tokens<1 then wait=math.max(wait,(1-tokens)*60000/rpm) end
 if wait>0 then redis.call('HINCRBY',KEYS[1],'rejected_rpm',1);return {0,math.ceil(wait),1,effective,recommended,count,minute} end
 tokens=tokens-1
 redis.call('HSET',KEYS[1],'tokens',tokens)
end
redis.call('ZADD',KEYS[2],now,ARGV[10]);redis.call('EXPIRE',KEYS[2],180)
redis.call('ZADD',KEYS[3],now,ARGV[10]);redis.call('EXPIRE',KEYS[3],120)
redis.call('HINCRBY',KEYS[1],'accepted',1)
return {1,0,0,effective,recommended,count+1,minute+1}
`)

func (c *accountTrafficCache) Acquire(ctx context.Context, p service.AccountTrafficPlan, id string) (service.AccountTrafficAdmission, error) {
	v := p.Policy
	flag := func(value bool) int {
		if value {
			return 1
		}
		return 0
	}
	values, err := trafficAcquireScript.Run(ctx, c.rdb, trafficKeys(p.AccountID), p.Revision, p.Signature, p.HardLimit, flag(v.StrictRPMEnabled), v.RPM, v.Burst, flag(v.AdaptiveEnabled), v.AdaptiveMode, v.MinConcurrency, id).Int64Slice()
	if err != nil {
		return service.AccountTrafficAdmission{}, err
	}
	if len(values) != 7 {
		return service.AccountTrafficAdmission{}, fmt.Errorf("invalid traffic admission result")
	}
	reasons := map[int64]string{1: "当前账号已达到本地 RPM 或突发额度，请稍后重试", 2: "当前账号已达到自适应并发上限，请稍后重试", 3: "账号流量配置已变化，请刷新后重试"}
	return service.AccountTrafficAdmission{Allowed: values[0] == 1, SkipObservation: values[2] == 4, RetryAfter: time.Duration(values[1]) * time.Millisecond, Reason: reasons[values[2]], State: service.AccountTrafficState{EffectiveConcurrency: int(values[3]), RecommendedConcurrency: int(values[4]), InFlight: int(values[5]), RequestsLastMinute: int(values[6])}}, nil
}

var trafficRefreshScript = redis.NewScript(`
local t=redis.call('TIME');local now=tonumber(t[1])*1000+tonumber(t[2])/1000
if redis.call('ZSCORE',KEYS[1],ARGV[1])==false then return 0 end
redis.call('ZADD',KEYS[1],now,ARGV[1]);redis.call('EXPIRE',KEYS[1],180);return 1
`)

func (c *accountTrafficCache) Refresh(ctx context.Context, id int64, lease string) (bool, error) {
	n, err := trafficRefreshScript.Run(ctx, c.rdb, []string{trafficKeys(id)[1]}, lease).Int()
	return n == 1, err
}

var trafficFinishScript = redis.NewScript(`
redis.replicate_commands()
if redis.call('ZREM',KEYS[2],ARGV[1])==0 then return 0 end
local status=tonumber(ARGV[2]);local duration=tonumber(ARGV[3])
if status>0 then redis.call('HINCRBY',KEYS[1],'completed',1);redis.call('HINCRBY',KEYS[1],'duration_ms',duration) end
if status==429 then redis.call('HINCRBY',KEYS[1],'upstream_429',1) end
if status>=500 and status<600 then redis.call('HINCRBY',KEYS[1],'upstream_5xx',1) end
if redis.call('HGET',KEYS[1],'signature')~=ARGV[4] or ARGV[5]~='1' then return 1 end
local t=redis.call('TIME');local now=tonumber(t[1])*1000+tonumber(t[2])/1000
local hard=tonumber(ARGV[6]);local minimum=math.min(tonumber(ARGV[7]),hard)
local threshold=tonumber(ARGV[8]);local window=tonumber(ARGV[9])*1000;local recovery=tonumber(ARGV[10])*1000
local limit=math.max(minimum,math.min(hard,tonumber(redis.call('HGET',KEYS[1],'recommended') or tostring(hard))))
local bad=status==429 or (status>=500 and status<600)
if bad then
 local begin=tonumber(redis.call('HGET',KEYS[1],'failure_window') or '0')
 local failures=tonumber(redis.call('HGET',KEYS[1],'failures') or '0')
 if now-begin>=window then begin=now;failures=0 end
 failures=failures+1
 redis.call('HSET',KEYS[1],'failure_window',begin,'failures',failures,'last_bad',now,'healthy_successes',0)
 local changed=tonumber(redis.call('HGET',KEYS[1],'last_adjustment') or '0')
 if failures>=threshold and now-changed>=10000 then
  local reduced=math.max(minimum,math.floor(limit/2))
  if reduced<limit then redis.call('HSET',KEYS[1],'recommended',reduced,'last_adjustment',now,'failures',0) end
 end
elseif status>=200 and status<300 then
 local good=redis.call('HINCRBY',KEYS[1],'healthy_successes',1)
 local badAt=tonumber(redis.call('HGET',KEYS[1],'last_bad') or redis.call('HGET',KEYS[1],'observed_since') or tostring(now))
 local changed=tonumber(redis.call('HGET',KEYS[1],'last_adjustment') or '0')
 if good>=3 and now-badAt>=recovery and now-changed>=recovery and limit<hard then
  redis.call('HSET',KEYS[1],'recommended',limit+1,'last_adjustment',now,'healthy_successes',0)
 end
end
redis.call('EXPIRE',KEYS[1],86400);return 1
`)

func (c *accountTrafficCache) Finish(ctx context.Context, p service.AccountTrafficPlan, id string, status int, duration int64) error {
	v := p.Policy
	enabled := 0
	if v.AdaptiveEnabled {
		enabled = 1
	}
	return trafficFinishScript.Run(ctx, c.rdb, trafficKeys(p.AccountID)[:2], id, status, duration, p.Signature, enabled, p.HardLimit, v.MinConcurrency, v.FailureThreshold, v.FailureWindowSeconds, v.RecoverySeconds).Err()
}

var trafficSyncScript = redis.NewScript(`
local old=tonumber(redis.call('HGET',KEYS[1],'revision') or '0')
if tonumber(ARGV[1])<old then return 0 end
redis.call('HSET',KEYS[1],'revision',ARGV[1],'signature',ARGV[2],'enforces',ARGV[3]);redis.call('EXPIRE',KEYS[1],86400);return 1
`)

func (c *accountTrafficCache) Sync(ctx context.Context, p service.AccountTrafficPlan) error {
	enforces := 0
	if p.Policy.Enforces() {
		enforces = 1
	}
	return trafficSyncScript.Run(ctx, c.rdb, trafficKeys(p.AccountID)[:1], p.Revision, p.Signature, enforces).Err()
}

func (c *accountTrafficCache) Snapshot(ctx context.Context, p service.AccountTrafficPlan) (service.AccountTrafficState, error) {
	keys := trafficKeys(p.AccountID)
	values, err := c.rdb.HGetAll(ctx, keys[0]).Result()
	if err != nil {
		return service.AccountTrafficState{}, err
	}
	number := func(key string) int64 { v, _ := strconv.ParseInt(values[key], 10, 64); return v }
	state := service.AccountTrafficState{EffectiveConcurrency: p.HardLimit, RecommendedConcurrency: int(number("recommended")), Accepted: number("accepted"), RejectedRPM: number("rejected_rpm"), RejectedConcurrency: number("rejected_concurrency"), Upstream429: number("upstream_429"), Upstream5xx: number("upstream_5xx"), Completed: number("completed")}
	if state.RecommendedConcurrency <= 0 {
		state.RecommendedConcurrency = p.HardLimit
	}
	if p.HardLimit > 0 && state.RecommendedConcurrency > p.HardLimit {
		state.RecommendedConcurrency = p.HardLimit
	}
	if p.HardLimit > 0 && state.RecommendedConcurrency < min(p.Policy.MinConcurrency, p.HardLimit) {
		state.RecommendedConcurrency = min(p.Policy.MinConcurrency, p.HardLimit)
	}
	if p.Policy.AdaptiveEnabled && p.Policy.AdaptiveMode == "automatic" {
		state.EffectiveConcurrency = state.RecommendedConcurrency
	}
	if state.Completed > 0 {
		state.AverageDurationMS = float64(number("duration_ms")) / float64(state.Completed)
	}
	if raw := values["last_adjustment"]; raw != "" {
		ms, _ := strconv.ParseFloat(raw, 64)
		value := time.UnixMilli(int64(ms))
		state.LastAdjustmentAt = &value
	}
	now, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return state, err
	}
	inflight, err := c.rdb.ZCount(ctx, keys[1], fmt.Sprint(now.Add(-120*time.Second).UnixMilli()), "+inf").Result()
	if err != nil {
		return state, err
	}
	minute, err := c.rdb.ZCount(ctx, keys[2], "("+fmt.Sprint(now.Add(-time.Minute).UnixMilli()), "+inf").Result()
	state.InFlight = int(inflight)
	state.RequestsLastMinute = int(minute)
	return state, err
}
