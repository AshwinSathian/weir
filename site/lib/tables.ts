import { repoFile } from './repo'

// Tables whose rows come from Go source. The value lists are read from the
// code; the one-line meanings are written here. A value with no meaning
// fails the build, so a new Cache-Status value or sentinel cannot ship
// undocumented.

function goStrings(src: string, decl: string): string[] {
  const m = new RegExp(`var ${decl} = \\[\\.\\.\\.\\]string\\{([\\s\\S]*?)\\n\\}`).exec(src)
  if (!m) throw new Error(`site: cachestatus.go has no ${decl}`)
  return [...m[1].matchAll(/"([^"]+)"/g)].map((x) => x[1])
}

function table(head: string[], rows: string[][]): string {
  return [`| ${head.join(' | ')} |`, `|${head.map(() => '---').join('|')}|`, ...rows.map((r) => `| ${r.join(' | ')} |`)].join('\n')
}

function explain(kind: string, values: string[], meaning: Record<string, string>): string[][] {
  return values.map((v) => {
    if (!meaning[v]) throw new Error(`site: ${kind} value "${v}" has no description in lib/tables.ts`)
    return ['`' + v + '`', meaning[v]]
  })
}

const fwd: Record<string, string> = {
  bypass: 'a `Bypass.Cookies` or `Bypass.Headers` rule matched, or the engine is in bypass mode',
  method: 'the method is not `GET` or `HEAD`',
  'uri-miss': 'nothing is stored for this URL',
  'vary-miss': 'something is stored for this URL, but not for these `Vary` header values',
  request: 'the client asked for revalidation and `Client.HonorRevalidation` is on',
  stale: 'the stored entry was stale and had to be refetched or revalidated',
}

const stale: Record<string, string> = {
  'stale-while-revalidate': 'inside the SWR window; a background refresh is running',
  'stale-if-error': 'the origin failed and the entry is inside its SIE window',
  shed: 'the limiter shed this request',
  'circuit-open': 'the breaker is open',
  'coalesce-timeout': 'this follower waited `Coalesce.FollowerMaxWait` for a slow flight',
}

const status: Record<string, string> = {
  ErrInvalidConfig: 'not a request error: returned by `New`, `SetMode` and `Purge`',
  ErrInvalidRequest: '400',
  ErrShed: '503 with `Retry-After`',
  ErrCircuitOpen: '503',
  ErrOriginTimeout: '504',
  ErrMustRevalidate: '504',
  ErrOnlyIfCached: '504',
  ErrOrigin: '502',
  ErrClosed: '503',
  ErrUpgradeNotSupported: '501',
  ErrEagerUnsupported: 'not a request error: returned by `Purge` with `Eager` on a store that cannot scrub',
}

/** Counts read from the threat model, so no page states a number by hand. */
export function modelCounts(): { threats: number; risks: number; invariants: number } {
  const tm = repoFile('docs/06-threat-model.md')
  const n = (re: RegExp) => new Set([...tm.matchAll(re)].map((m) => m[1])).size
  const c = { threats: n(/^\| (T-\d+) \|/gm), risks: n(/^- (R-\d+)\./gm), invariants: n(/^- (INV-\d+) /gm) }
  if (!c.threats || !c.risks || !c.invariants) throw new Error('site: could not count threats, risks or invariants in docs/06')
  return c
}

export function generatedTables(): Record<string, string> {
  const counts = modelCounts()
  const cs = repoFile('cachestatus.go')
  const errs = [...repoFile('errors.go').matchAll(/^\t(Err\w+) = errors\.New\("([^"]+)"\)/gm)]
  if (errs.length === 0) throw new Error('site: errors.go has no sentinels')
  return {
    threat_count: String(counts.threats),
    risk_count: String(counts.risks),
    invariant_count: String(counts.invariants),
    fwd_table: table(['Value', 'The origin was asked because'], explain('fwd', goStrings(cs, 'fwdNames'), fwd)),
    stale_table: table(['Value', 'Stale content was served because'], explain('detail', goStrings(cs, 'staleDetails'), stale)),
    errors_table: table(
      ['Sentinel', 'Message', 'Status'],
      errs.map(([, name, msg]) => {
        if (!status[name]) throw new Error(`site: ${name} has no status in lib/tables.ts`)
        return ['`' + name + '`', '`' + msg + '`', status[name]]
      }),
    ),
  }
}
