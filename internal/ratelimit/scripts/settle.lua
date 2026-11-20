-- Settle a reservation once the real usage is known. Refunds reserved - used
-- (never above capacity), or charges the extra if used > reserved, in which
-- case the balance may go below zero (overshoot).
--
-- KEYS[1]  budget key, for example budget:support-bot
-- KEYS[2]  settled marker, for example settled:<request id>
-- ARGV[1]  capacity
-- ARGV[2]  refill rate (tokens per second)
-- ARGV[3]  reserved tokens
-- ARGV[4]  used tokens
-- ARGV[5]  marker TTL in seconds
-- ARGV[6]  current time in seconds; empty string means use the Redis clock
--
-- Returns 1 if applied, 0 if this request was already settled (idempotent).

if not redis.call('SET', KEYS[2], '1', 'NX', 'EX', tonumber(ARGV[5])) then
  return 0
end

local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local reserved = tonumber(ARGV[3])
local used = tonumber(ARGV[4])

local now
if ARGV[6] ~= nil and ARGV[6] ~= '' then
  now = tonumber(ARGV[6])
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

if now > ts then
  tokens = math.min(capacity, tokens + (now - ts) * rate)
  ts = now
end

tokens = tokens + (reserved - used)
if tokens > capacity then
  tokens = capacity
end

redis.call('HSET', KEYS[1], 'tokens', string.format('%.6f', tokens), 'ts', string.format('%.6f', ts))
return 1
