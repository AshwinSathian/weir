package valkey

import "github.com/valkey-io/valkey-go"

// The two epoch scripts (05 §7). Every epoch key is declared in KEYS on every
// call, so cluster mode accepts them and the {e} hash tag puts them in one
// slot. Argument positions are the arg* constants in meta.go.
//
// KEYS: 1 hardidx, 2 global, 3 sketch:soft, 4 sketch:invalid, 5 newest, 6 meta.
//
// Loss (05 §7): meta has no field v, or a sketch plane is absent. The planes
// are created full-size by the writing script, 2^19 u32 cells each (E-9), so
// an absent plane is never the normal state. An absent hardidx is.

// writeEpochSrc raises one epoch to a maximum.
//
//	ARGV 1 mode (1 soft, 2 invalid, 3 hard)  2 '1' for the global tag
//	     3 tag (32 raw bytes)                4 At, whole seconds in [1, 2^32-1]
//	     5 prune margin, seconds             6 hard-epoch cap
//	     7 '1' to use ceil(server time) as At (the loss repair)
//	     8, 9 cell positions                 10 first 8 bytes of the seed
//
// A soft or invalid write on a non-global tag first checks the seed and
// replies SEED_CHANGED with no effect if meta.seed differs: after a flush the
// positions were computed from a seed the server no longer has. A write that
// finds a loss repairs it before doing its own work: it recreates meta.v and
// the planes and raises the global hard epoch to server time, so recreating
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
local mode = tonumber(ARGV[1])
local sketch = ARGV[2] ~= '1' and mode < 3
if sketch then
  local seed = redis.call('HGET', KEYS[6], 'seed')
  if seed == false or string.sub(seed, 1, 8) ~= ARGV[10] then
    return redis.error_reply('SEED_CHANGED')
  end
end
local t = redis.call('TIME')
local now = tonumber(t[1])
local now_up = now
if tonumber(t[2]) > 0 then now_up = now + 1 end
local lost = redis.call('HEXISTS', KEYS[6], 'v') == 0
for i = 3, 4 do
  if redis.call('EXISTS', KEYS[i]) == 0 then
    redis.call('SETRANGE', KEYS[i], 2097151, '\0')
    lost = true
  end
end
if lost then
  redis.call('HSET', KEYS[6], 'v', 1)
  raise_hash(KEYS[2], 'hard', now_up)
  raise_newest(now_up)
end
local at = tonumber(ARGV[4])
if ARGV[7] == '1' then at = now_up end
if ARGV[2] == '1' then
  raise_hash(KEYS[2], names[mode], at)
elseif sketch then
  local key = KEYS[2 + mode]
  for i = 8, 9 do
    local cell = '#' .. ARGV[i]
    local cur = redis.call('BITFIELD', key, 'GET', 'u32', cell)[1]
    if cur < at then
      redis.call('BITFIELD', key, 'OVERFLOW', 'SAT', 'SET', 'u32', cell, at)
    end
  end
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
// skew reaches since. It is read-only (EVALSHA_RO) and replies {-1} on loss,
// {-2} when the caller's seed differs from meta.seed, {} for no epoch and
// {mode, at} otherwise. It never reads newest, so an absent newest is not
// read as "no epochs" (05 §7). Loss is checked before the seed: after a
// flush the repair comes first, then the seed.
//
//	ARGV 1 since, whole seconds, rounded down  2 skew, whole seconds
//	     3 global tag: '0' not named, '1' named, '2' named as a shared tag
//	     4 first 8 bytes of the seed
//	     5.. per other tag, four values: 32 raw bytes, two cell positions,
//	         '1' if the tag is shared (E-12)
//
// A shared tag is read in the soft plane and the hard table but never in the
// invalid plane, nor is a shared global tag's invalid field.
const readEpochSrc = `
if redis.call('HEXISTS', KEYS[6], 'v') == 0 or redis.call('EXISTS', KEYS[3]) == 0
    or redis.call('EXISTS', KEYS[4]) == 0 then
  return {-1}
end
if #ARGV >= 5 then
  local seed = redis.call('HGET', KEYS[6], 'seed')
  if seed == false or string.sub(seed, 1, 8) ~= ARGV[4] then return {-2} end
end
local since = tonumber(ARGV[1])
local skew = tonumber(ARGV[2])
local best_mode, best_at = 0, 0
local function cand(m, v)
  if v ~= nil and v > 0 and v + skew >= since
      and (m > best_mode or (m == best_mode and v > best_at)) then
    best_mode, best_at = m, v
  end
end
local function cells(key, p1, p2)
  local c = redis.call('BITFIELD_RO', key, 'GET', 'u32', '#' .. p1, 'GET', 'u32', '#' .. p2)
  return math.min(c[1], c[2])
end
if ARGV[3] ~= '0' then
  local g = redis.call('HMGET', KEYS[2], 'soft', 'invalid', 'hard')
  cand(1, tonumber(g[1]))
  if ARGV[3] == '1' then cand(2, tonumber(g[2])) end
  cand(3, tonumber(g[3]))
end
for i = 5, #ARGV, 4 do
  cand(3, tonumber(redis.call('ZSCORE', KEYS[1], ARGV[i])))
  cand(1, cells(KEYS[3], ARGV[i + 1], ARGV[i + 2]))
  if ARGV[i + 3] ~= '1' then cand(2, cells(KEYS[4], ARGV[i + 1], ARGV[i + 2])) end
end
if best_mode == 0 then return {} end
return {best_mode, best_at}
`

// The scripts are immutable once built; valkey-go keeps their SHA-1.
var (
	writeEpochScript = valkey.NewLuaScript(writeEpochSrc)
	readEpochScript  = valkey.NewLuaScriptReadOnly(readEpochSrc)
)
