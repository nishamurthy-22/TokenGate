-- Atomically refill one application's token bucket and reserve tokens from it.
-- Redis runs the whole script as one step, so concurrent gateways can never
-- spend the same tokens twice.
--
-- KEYS[1]  budget key, for example budget:support-bot
-- ARGV[1]  capacity (maximum tokens in the bucket)
-- ARGV[2]  refill rate (tokens per second)
-- ARGV[3]  cost (tokens to reserve)
-- ARGV[4]  current time in seconds; empty string means use the Redis clock
--
-- Returns {1, balance} if the tokens were reserved, {0, balance} if not.

local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])

local now
if ARGV[4] ~= nil and ARGV[4] ~= '' then
  now = tonumber(ARGV[4])
else
  local t = redis.call('TIME')
  now = tonumber(t[1]) + tonumber(t[2]) / 1000000
end

local state = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(state[1])
local ts = tonumber(state[2])
if tokens == nil or ts == nil then
  tokens = capacity
  ts = now
end

-- Refill for the time passed since the last update. If the caller's clock is
-- behind the stored timestamp (clock drift between gateways), nothing is refilled.
if now > ts then
  tokens = math.min(capacity, tokens + (now - ts) * rate)
  ts = now
end

local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
end

redis.call('HSET', KEYS[1], 'tokens', string.format('%.6f', tokens), 'ts', string.format('%.6f', ts))
return {allowed, string.format('%.6f', tokens)}
