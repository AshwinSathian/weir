package valkey

import "github.com/valkey-io/valkey-go"

// The two epoch scripts (05 §7). Every epoch key is declared in KEYS on every
// call, so cluster mode accepts them and the {e} hash tag puts them in one
// slot. Argument positions are the arg* constants in meta.go.
//
// KEYS: 1 hardidx, 2 global, 3 sketch:soft, 4 sketch:invalid, 5 newest, 6 meta.
// The sketch planes are declared now and used from P25-03b.

// writeEpochSrc raises one epoch to a maximum.
//
//	ARGV 1 mode (1 soft, 2 invalid, 3 hard)  2 '1' for the global tag
//	     3 tag (32 raw bytes)                4 At, whole seconds in [1, 2^32-1]
//	     5 prune margin, seconds             6 hard-epoch cap
//	     7 '1' to use ceil(server time) as At (the loss repair)
//
// A write that finds meta absent is a loss repair too (05 §7): it raises the
// global hard epoch to server time before doing its own work, so recreating
// meta never hides a loss. Every effect is a max or a replace, so the script
// is idempotent and the store may retry it (05 §7, Timeouts).
const writeEpochSrc = `
local function raise_hash(key, field, at)
  local cur = tonumber(redis.call('HGET', key, field))
  if cur == nil or cur < at then redis.call('HSET', key, field, at) end
end
local function raise_newest(at)
  local cur = tonumber(redis.call('GET', KEYS[5]))
  if cur == nil or cur < at then redis.call('SET', KEYS[5], at) end
end
local names = {'soft', 'invalid', 'hard'}
local t = redis.call('TIME')
local now = tonumber(t[1])
local now_up = now
if tonumber(t[2]) > 0 then now_up = now + 1 end
if redis.call('EXISTS', KEYS[6]) == 0 then
  redis.call('HSET', KEYS[6], 'v', 1)
  raise_hash(KEYS[2], 'hard', now_up)
  raise_newest(now_up)
end
local mode = tonumber(ARGV[1])
local at = tonumber(ARGV[4])
if ARGV[7] == '1' then at = now_up end
if ARGV[2] == '1' then
  raise_hash(KEYS[2], names[mode], at)
else
  redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', '(' .. (now - tonumber(ARGV[5])))
  if redis.call('ZSCORE', KEYS[1], ARGV[3]) == false
      and redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[6]) then
    return redis.error_reply('WEIRCAP hard epoch table is full')
  end
  redis.call('ZADD', KEYS[1], 'GT', at, ARGV[3])
end
raise_newest(at)
return 1
`

// readEpochSrc finds the most severe, then newest, epoch whose At plus the
// skew reaches since. It is read-only (EVALSHA_RO) and replies {-1} when meta
// is absent, {} for no epoch and {mode, at} otherwise. It never reads newest,
// so an absent newest is not read as "no epochs" (05 §7).
//
//	ARGV 1 since, whole seconds, rounded down  2 skew, whole seconds
//	     3 '1' if the lookup names the global tag
//	     4.. the other tags (32 raw bytes each)
const readEpochSrc = `
if redis.call('EXISTS', KEYS[6]) == 0 then return {-1} end
local since = tonumber(ARGV[1])
local skew = tonumber(ARGV[2])
local best_mode, best_at = 0, 0
local function cand(m, v)
  if v ~= nil and v + skew >= since and (m > best_mode or (m == best_mode and v > best_at)) then
    best_mode, best_at = m, v
  end
end
if ARGV[3] == '1' then
  local g = redis.call('HMGET', KEYS[2], 'soft', 'invalid', 'hard')
  for m = 1, 3 do cand(m, tonumber(g[m])) end
end
for i = 4, #ARGV do
  cand(3, tonumber(redis.call('ZSCORE', KEYS[1], ARGV[i])))
end
if best_mode == 0 then return {} end
return {best_mode, best_at}
`

// The scripts are immutable once built; valkey-go keeps their SHA-1.
var (
	writeEpochScript = valkey.NewLuaScript(writeEpochSrc)
	readEpochScript  = valkey.NewLuaScriptReadOnly(readEpochSrc)
)
