// Charts for the benchmarks and compare pages. Static inline SVG in the
// vocabulary of diagrams.tsx. Every number drawn here comes from
// lib/results.ts, which parses docs/benchmarks.md: this file types none, and
// scripts/check.mjs fails on a digit in its copy. Each chart is drawn twice,
// for a wide and a narrow column, and CSS shows one, so a phone gets text at
// full size instead of a scaled-down picture.

import Link from 'next/link'
import { repoFile } from '@/lib/repo'
import { DOC, HEAD, NA, SOUIN, SUBJECTS, col, headlines, parseResults, probeCol, type Cell, type Figure, type Tier } from '@/lib/results'
import { Svg } from './diagrams'

const R = parseResults(repoFile(DOC))
const H = headlines(R)
const W = SUBJECTS[0]
/** A column title that fits: the tables name cache-handler with the project it is built on. */
const short = (s: string) => (s === SOUIN ? 'cache-handler' : s)
const fmt = (n: number) => n.toLocaleString('en-US')
const bare = (s: string) => s.replaceAll(String.fromCharCode(96), '') // code spans in table labels
/** The spread of the repetitions, where the table gives one. */
const range = (c: Cell) => (c.lo === undefined || c.hi === undefined ? undefined : fmt(c.lo) + ' to ' + fmt(c.hi))
const shown = (c: Cell) => (c.na ? NA : fmt(c.v))

type Lay = { narrow: boolean; w: number; label: number }
const G = { wide: 688, slim: 326, num: 44, bar: 10, sm: 14, chars: 50, glyph: 6.7, wideGlyph: 7.5, sub: 16 }
const lays = (label: number): Lay[] => [
  { narrow: false, w: G.wide, label },
  { narrow: true, w: G.slim, label: 0 },
]
/** SVG text does not wrap: break a label into lines of at most `max` characters, at spaces. */
function wrap(text: string, max: number): string[] {
  const lines: string[] = []
  for (const word of text.split(' ')) {
    const last = lines[lines.length - 1]
    if (last !== undefined && (last + ' ' + word).length <= max) lines[lines.length - 1] = last + ' ' + word
    else lines.push(word)
  }
  return lines
}
type Val = { v: number; na?: boolean; flag?: string }
type Col = { key: string; title: string; own: boolean }
type Row = {
  n: (c: string) => string
  name: string
  what: string
  right?: string
  of?: number
  mode: 'bar' | 'served' | 'expected' | 'plain'
  get: (c: string) => Val
  group?: string
  note?: string
}

/** Rows of scenarios against columns of caches. Each cell is a count and, where the row has a total, a bar that is its share of that total. */
function Grid({ id, title, desc, cols, rows, lay, flags }: { id: string; title: string; desc: string; cols: Col[]; rows: Row[]; lay: Lay; flags: boolean }) {
  const cw = (lay.w - lay.label) / cols.length
  const out: React.ReactNode[] = []
  let y = 0
  if (!lay.narrow) {
    cols.forEach((c, i) => out.push(<text key={c.key} className={c.own ? 'acc strong' : 'ink strong'} x={lay.label + i * cw + 8} y={y + G.sm}>{c.title}</text>))
    y += 26
  }
  let group = ''
  rows.forEach((r, ri) => {
    if (r.group && r.group !== group) {
      group = r.group
      out.push(<line key={'l' + ri} className="axis" x1={0} y1={y + 4} x2={lay.w} y2={y + 4} />, <text key={'g' + ri} className="mut sm caps" x={0} y={y + 22}>{group}</text>)
      y += 32
    }
    const top = y
    // Wide: the total shares the second line with the description, so a long name cannot run into it.
    const second = !lay.narrow && r.right ? r.right + ', ' + r.what : r.what
    // The description stays inside its column: a long one takes more lines, and the row grows, so it
    // cannot run under the first cache's number or the range printed below it.
    const lines = wrap(second, lay.narrow ? G.chars : Math.floor(lay.label / G.glyph))
    const extra = (lines.length - 1) * G.sm
    const cellTop = lay.narrow ? top + 34 + extra : top
    out.push(
      <text key={'n' + ri} className="ink" x={0} y={top + G.sm}>{r.name}</text>,
      ...lines.map((l, li) => <text key={'w' + ri + '-' + li} className="mut sm" x={0} y={top + 29 + li * G.sm}>{l}</text>),
      lay.narrow && r.right && <text key={'r' + ri} className="mut sm" x={lay.w} y={top + G.sm} textAnchor="end">{r.right}</text>,
    )
    cols.forEach((c, i) => {
      const cx = lay.label + i * cw
      const { v, na, flag } = r.get(c.key)
      const tx = lay.narrow ? cx : cx + G.num + 6
      const tw = lay.narrow ? cw - 10 : cw - G.num - 20
      const ty = lay.narrow ? cellTop + 34 : cellTop + 5
      const len = r.of && v && !na ? Math.max(2, Math.min(1, v / r.of) * tw) : 0
      const who = c.own ? 'weir' : 'other'
      const rest = tw - len
      const shape = { bar: 'b-', plain: 'b-', served: 'o-', expected: 'e-' }[r.mode] + who
      out.push(
        <g key={ri + c.key}>
          {lay.narrow && <text className={c.own ? 'acc sm' : 'mut sm'} x={cx} y={cellTop + 12}>{c.title}</text>}
          {/* A cache that cannot run the scenario gets the words, never a zero and never a bar. */}
          {na ? (
            // Narrow: the words are a little wider than the column, so they are fitted to it.
            <text className="mut sm" data-n={r.n(c.key)} x={cx + (lay.narrow ? 0 : 8)} y={lay.narrow ? cellTop + 28 : cellTop + G.sm} {...(lay.narrow ? { textLength: cw - 12, lengthAdjust: 'spacingAndGlyphs' } : {})}>{NA}</text>
          ) : (
            <text className={v || r.mode !== 'bar' ? 'ink' : 'mut'} data-n={r.n(c.key)} x={lay.narrow ? cx : cx + G.num} y={lay.narrow ? cellTop + 28 : cellTop + G.sm} textAnchor={lay.narrow ? 'start' : 'end'}>{fmt(v)}</text>
          )}
          {r.of && !na ? <rect className="track" x={tx} y={ty} width={tw} height={G.bar} rx={2} /> : null}
          {len ? <rect className={shape} x={tx} y={ty} width={len} height={G.bar} rx={2} /> : null}
          {/* A served row: what was not served is the filled part, so a zero is a full bar and not an empty track. */}
          {r.mode === 'served' && rest && !na ? <rect className={'b-' + who} x={tx + len} y={ty} width={rest} height={G.bar} rx={2} /> : null}
          {flag && <text className="ink sm" x={lay.narrow ? tx : cx + 8} y={ty + G.bar + 13}>{flag}</text>}
        </g>,
      )
    })
    y = lay.narrow ? cellTop + 46 + (flags ? G.sm : 0) : top + 34 + Math.max(extra, flags ? G.sm : 0)
    if (r.note) {
      out.push(<text key={'o' + ri} className="ink sm" x={0} y={y + 8}>{r.note}</text>)
      y += 18
    }
    y += 10
  })
  return (
    <Svg id={id} w={lay.w} h={y} title={title} desc={desc}>
      {out}
    </Svg>
  )
}

const both = (label: number, draw: (lay: Lay, suffix: string) => React.ReactNode) =>
  lays(label).map((lay) => (
    <div key={String(lay.narrow)} className={lay.narrow ? 'only-slim' : 'only-wide'}>{draw(lay, lay.narrow ? '-s' : '-w')}</div>
  ))

type TierTab = { tier: Tier; name: string; says: string }
const TIERS: TierTab[] = [
  { tier: 'default', name: 'Default', says: 'Default tier: the smallest configuration that turns caching on.' },
  { tier: 'tuned', name: 'Tuned', says: 'Tuned tier: each project’s documented stampede and stale settings.' },
]

/** Both tiers are in the page. Two radio buttons and CSS show one; with no CSS, or on paper, both show. */
function Tiered({ id, label, draw, tabs = TIERS }: { id: string; label: number; draw: (tier: Tier, lay: Lay, id: string) => React.ReactNode; tabs?: TierTab[] }) {
  return (
    <div className="tiers" role="group" aria-label="Configuration tier">
      {tabs.map((t, i) => <input key={t.tier} className="sr-only" type="radio" name={id} id={id + '-' + t.tier} defaultChecked={!i} />)}
      <div className="toggle">
        <span className="label">Configuration</span>
        {tabs.map((t) => <label key={t.tier} htmlFor={id + '-' + t.tier}>{t.name}</label>)}
      </div>
      {tabs.map((t) => (
        <div key={t.tier} className={'tier ' + t.tier}>
          <p className="tier-name">{t.says}</p>
          {both(label, (lay, s) => draw(t.tier, lay, id + '-' + t.tier + s))}
        </div>
      ))}
    </div>
  )
}

const tierCols = (tier: Tier): Col[] => SUBJECTS.map((s) => ({ key: col(s, tier), title: short(s), own: s === W }))

/** R4a of the comparison spec: the statement travels with every figure that shows another project. */
function Unverified() {
  return (
    <p className="unverified">
      Three rounds on one machine, the third on {R.date}. These are our own runs with configurations we wrote. They have not been verified by the owners of
      Varnish, nginx or cache-handler.
    </p>
  )
}

/** The same statement, short, above the chart: on a phone the caption is two screens below the first bar. */
function Ours() {
  return <p className="unverified">Our own runs on {R.date}, with configurations we wrote. Not verified by the owners of Varnish, nginx or cache-handler.</p>
}

/** The line under a number when the repetitions disagreed. */
const Spread = () => (
  <p>
    Each number is the median of the repetitions. Where they differed by more than a tenth, the range is printed under it.
  </p>
)

// What each scenario is, in a few words. A scenario the tables gain must be described here, or the build fails.
const HERD = 'One key, many clients'
const KEYS = 'Many keys at once'
const UNSTORED = 'Responses that are not stored'
const SCEN: Record<string, { group: string; what: string; expected?: boolean }> = {
  'cold-herd': { group: HERD, what: 'nothing stored yet' },
  'slow-origin-herd': { group: HERD, what: 'a very slow origin' },
  'leader-abort': { group: HERD, what: 'first client disconnects' },
  'cold-keys': { group: KEYS, what: 'nothing stored yet' },
  'cold-keys-capped': { group: KEYS, what: 'origin refuses overload' },
  'sync-expiry': { group: KEYS, what: 'all expire together' },
  'sync-expiry-capped': { group: KEYS, what: 'origin refuses overload' },
  'uncacheable-herd': { group: UNSTORED, what: 'marked private' },
  'turned-private': { group: UNSTORED, what: 'stored, then private' },
  'error-herd': { group: UNSTORED, what: 'origin only sends errors', expected: true },
  'oversize-herd': { group: UNSTORED, what: 'over the size limit' },
  'large-body-herd': { group: UNSTORED, what: 'a large body' },
}
const EXPECTED = 'Every client failing is the correct result here.'

function scenarioRows(kind: 'failed' | 'calls'): Row[] {
  return R[kind].map((s, i) => {
    const meta = SCEN[s.name]
    if (!meta) throw new Error('site: scenario "' + s.name + '" has no description in components/charts.tsx')
    const lost = R.failed[i]
    return {
      n: (c) => [kind, s.label, c].join('|'),
      name: s.name,
      what: meta.what,
      right: fmt(s.clients) + ' clients',
      of: s.clients,
      group: meta.group,
      mode: kind === 'failed' && meta.expected ? 'expected' : 'bar',
      note: meta.expected ? EXPECTED : undefined,
      get: (c) => {
        const cell = s.cells[c]
        if (kind === 'failed') return { v: cell.v, flag: range(cell) }
        const f = lost.cells[c]
        const cross = f.v && !meta.expected ? '✕ ' + fmt(f.v) : undefined
        return { v: cell.v, flag: [range(cell), cross].filter(Boolean).join('  ') || undefined }
      },
    }
  })
}

function FailedChart() {
  const rows = scenarioRows('failed')
  return (
    <figure className="chart" data-others>
      <Ours />
      <Tiered id="failed" label={224} draw={(tier, lay, id) => (
        <Grid id={id} lay={lay} cols={tierCols(tier)} rows={rows} flags title={'Clients that did not get a 2xx, ' + tier + ' tier'}
          desc="One row per scenario, one column per cache. Each cell is the count of clients that failed and a bar for its share of the scenario's clients. The full table follows the chart." />
      )} />
      <figcaption>
        <p>
          A bar is the share of that scenario’s clients that failed, and the number is the count. Any count above zero draws at least a sliver. Lower is
          better, with one exception: in <code>error-herd</code> the origin only sends errors, so every client failing is the correct result. Those
          bars have a dashed outline and no fill. A refusal that a cache sends to protect the origin counts as a failure, for Weir as for the others.
        </p>
        <p>
          Weir’s default has an origin limiter and the other defaults have none. Varnish’s tuned tier has its backend capped with Weir’s default
          limiter numbers, so in the capped rows the tuned Weir and Varnish columns compare two configurations of one idea.
        </p>
        <p>
          A success with the wrong body counts as a failure. cache-handler answers a success with an empty body to clients that waited on a fetch
          it does not store, which is why it fails every client of <code>error-herd</code> and most of <code>cold-keys-capped</code>. It gave up on
          the very slow origin at its own timeout, and refused the followers of a client that disconnected.
        </p>
        <Spread />
        <Unverified />
      </figcaption>
    </figure>
  )
}

function CallsChart() {
  const rows = scenarioRows('calls')
  return (
    <figure className="chart" data-others>
      <Ours />
      <Tiered id="calls" label={224} draw={(tier, lay, id) => (
        <Grid id={id} lay={lay} cols={tierCols(tier)} rows={rows} flags title={'Requests that reached the origin, ' + tier + ' tier'}
          desc="One row per scenario, one column per cache. Each cell is the count of origin calls and a bar for its share of the scenario's clients, with a mark where clients failed in the same run. The full table follows the chart." />
      )} />
      <figcaption>
        <p>
          A bar is origin calls as a share of that scenario’s clients, and it stops at one call per client. Any count above zero draws at least a sliver.
          Fewer calls is better only when nobody failed. A ✕ gives the clients that failed in the same run, so a short bar with a ✕ is an origin that refused, not a cache that saved work.
        </p>
        <p>
          In the expiry rows the keys are asked for twice, and a cache that serves stale copies refreshes them in the background, so a tuned
          tier’s count includes those refreshes. Weir’s tuned tier refreshes every expired key, a few at a time, as Varnish’s and nginx’s do.
        </p>
        <Spread />
        <Unverified />
      </figcaption>
    </figure>
  )
}

const figureRows = (key: 'recovery' | 'flood' | 'changed', set: Figure[], groups?: Record<string, string>): Row[] =>
  set.flatMap((f) =>
    f.measures.map((m, i): Row => {
      if (groups && !groups[f.scenario]) throw new Error('site: scenario "' + f.scenario + '" has no group in components/charts.tsx')
      return {
        n: (c) => [key, f.label, c, i].join('|'),
        name: f.scenario,
        what: m.raw,
        of: m.of,
        group: groups?.[f.scenario],
        mode: m.served ? 'served' : m.of ? 'bar' : 'plain',
        get: (c) => ({ v: f.cells[c][i].v, na: f.cells[c][i].na, flag: range(f.cells[c][i]) }),
      }
    }),
  )

function RecoveryChart() {
  const rows = figureRows('recovery', R.recovery)
  return (
    <figure className="chart" data-others>
      <Ours />
      <Tiered id="recovery" label={220} draw={(tier, lay, id) => (
        <Grid id={id} lay={lay} cols={tierCols(tier)} rows={rows} flags title={'Failure and recovery scenarios, ' + tier + ' tier'}
          desc="One row per figure, one column per cache: bystanders failed beside a failing route, clients failed and origin calls during an origin outage, clients served after it, stored entries lost to a flood of unsafe requests, and how many of those requests were answered. The full table follows the chart." />
      )} />
      <figcaption>
        <p>
          Filled always means clients failed, entries lost or clients not served, so less fill is better in every row. In the two rows that count
          clients served after the origin came back, the outlined part is the clients served and the filled part is the rest. Origin calls while
          down has no fixed total, so it is a number with no bar, and a low number there is no win in a column where every client failed. The
          last row is a check on the one above it: every cache answered the whole flood, so a zero for entries lost is a flood survived and not
          a flood refused.
        </p>
        <p>
          cache-handler’s tuned stale setting showed no effect here, and by hand it made no difference whether the origin sent stale
          directives of its own. We do not know what the setting needs, so its tuned outage cells describe our configuration.
        </p>
        <Spread />
        <Unverified />
      </figcaption>
    </figure>
  )
}

const FLOOD_GROUPS: Record<string, string> = {
  'busting-flood': 'One path flooded beside ordinary traffic',
  brownout: 'An origin that fails in bursts',
  'purge-herd': 'A purge of every stored page',
  restart: 'The cache is stopped and started',
}

function FloodChart() {
  const rows = figureRows('flood', R.flood, FLOOD_GROUPS)
  return (
    <figure className="chart" data-others>
      <Ours />
      <Tiered id="flood" label={250} draw={(tier, lay, id) => (
        <Grid id={id} lay={lay} cols={tierCols(tier)} rows={rows} flags title={'Flood, brownout, purge and restart, ' + tier + ' tier'}
          desc="One row per figure, one column per cache, in four groups: a flood of one path beside ordinary traffic, an origin failing in bursts, a purge of every stored page, and a restart of the cache. The full table follows the chart." />
      )} />
      <figcaption>
        <p>
          Filled means clients failed, old content served or entries lost, so less fill is better in every row that has a bar. Rows with no
          bar have no fixed total: origin calls in the bursts, the most requests the origin had at once, and seconds. A cache that cannot run a
          scenario says so in words and gets no bar: open source nginx has no purge.
        </p>
        <p>
          <strong>Weir still loses a row in every group.</strong> In the flood it serves every bystander and fails more clients in total than any
          other column, because it refuses the flooded path while the origin still has room. In the brownout its default tier keeps refusing
          for a moment after the origin is back. After a purge with stale serving on, some pages still show their old content at the last read.
          After a restart it asks the origin for every page again, where nginx asks for none.
        </p>
        <p>
          The release measured here narrowed the first three and added a setting for the last, which is off by default and not used by the
          tuned tier. The chart under what the third round changed compares the two releases.
        </p>
        <Spread />
        <Unverified />
      </figcaption>
    </figure>
  )
}

function ChangedChart() {
  const rows = figureRows('changed', R.changed)
  return (
    <figure className="chart">
      {both(250, (lay, s) => (
        <Grid id={'changed' + s} lay={lay} rows={rows} flags={false} title="What the third round changed in Weir"
          cols={HEAD.changed.slice(1).map((c) => ({ key: c, title: c, own: true }))}
          desc="One row per figure that moved, two columns: the release the second round measured and the release the third round measured. The full table follows the chart." />
      ))}
      <figcaption>
        <p>
          Weir only, behind Caddy, every figure that differs between the two rounds. A bar is a share of the total its row names. Fewer
          failures and fewer pages left stale are better; more origin calls are the point in the expiry, purge, flood and over-size rows,
          because each is a refresh or a download that the older release dropped or repeated, and a cost in the outage row. The scenarios,
          the harness logic and the other caches’ configurations were the same in both rounds.
        </p>
        <p>The third round on one machine, {R.date}, against the second.</p>
      </figcaption>
    </figure>
  )
}

type Bar = { n: string; label: string; v: number; tail: string; own: boolean; sub?: string }

function hasSub(b: Bar) {
  return Boolean(b.sub)
}

/** Horizontal bars from zero, each under its label. */
function Bars({ id, title, desc, bars, max, lay }: { id: string; title: string; desc: string; bars: Bar[]; max: number; lay: Lay }) {
  const tw = lay.narrow ? lay.w : lay.w - 230
  // A second, small line under a bar, for what does not fit beside it.
  const sub = bars.some(hasSub) ? G.sub : 0
  const step = (lay.narrow ? 62 : 46) + sub
  return (
    <Svg id={id} w={lay.w} h={bars.length * step + 4} title={title} desc={desc}>
      {bars.map((b, i) => {
        const y = i * step
        const len = (b.v / max) * tw
        return (
          <g key={b.label}>
            <text className="ink" x={0} y={y + G.sm}>{b.label}</text>
            <rect className="track" x={0} y={y + 22} width={tw} height={G.sm} rx={2} />
            <rect className={b.own ? 'b-weir' : 'b-other'} x={0} y={y + 22} width={len} height={G.sm} rx={2} />
            <text className="ink" x={lay.narrow ? 0 : tw + 10} y={lay.narrow ? y + 52 : y + 34}>
              <tspan data-n={b.n}>{fmt(b.v)}</tspan>
              {b.tail}
            </text>
            {b.sub && <text className="mut sm" x={0} y={y + (lay.narrow ? 68 : 50)}>{b.sub}</text>}
          </g>
        )
      })}
    </Svg>
  )
}

function ConformanceChart() {
  const bars = R.conformance.map((c): Bar => ({
    n: ['conformance', c.row, 'Pass'].join('|'),
    label: bare(c.subject) + ', ' + c.tier,
    v: c.pass,
    tail: ' of ' + fmt(R.total) + ' pass',
    sub: fmt(c.assertion) + ' fail an assertion, ' + fmt(c.setup) + ' never reach one',
    own: c.subject.startsWith('Weir'),
  }))
  return (
    <figure className="chart" data-others>
      <Ours />
      {both(0, (lay, s) => (
        <Bars id={'conf' + s} lay={lay} bars={bars} max={R.total} title="cache-tests passed by each cache, in each configuration"
          desc={bars.map((b) => b.label + ': ' + fmt(b.v) + b.tail + '; ' + b.sub).join('. ')} />
      ))}
      <figcaption>
        <p>
          The bars start at zero and end at the whole suite. The suite mixes required and optional behavior. A test that did not pass either
          failed its assertion, or never reached it because its setup failed, which says nothing about the behavior under test. Neither is always
          a violation. Weir’s are explained on the <Link href="/conformance/">conformance page</Link>. The other projects’ are not analyzed at all.
          Weir forwards the suite’s own request headers (<code>forward allow</code>), as its baseline run does. The suite’s authors say its
          results should not be used to compare feature support, so read the bars as separate results and not as a ranking.
        </p>
        <p>
          Weir’s tuned tier passes fewer tests than its default. It turns stale serving on for every response, so the suite’s second request
          is answered from the stale copy while the refresh runs, and tests that expect the refreshed response fail. That is the cost of
          choosing stale serving for responses whose origin did not ask for it.
        </p>
        <Unverified />
      </figcaption>
    </figure>
  )
}

function HitChart() {
  const bars = R.hit.map((h, i): Bar => ({
    n: ['hit', '`BenchmarkServeHitSmall`', h.build].join('|'),
    label: bare(h.build),
    v: h.ns,
    tail: ' ns, ' + h.allocs + ' allocations',
    own: i === R.hit.length - 1,
  }))
  return (
    <figure className="chart">
      {both(0, (lay, s) => (
        <Bars id={'hit' + s} lay={lay} bars={bars} max={Math.max(...bars.map((b) => b.v))} title="One cached hit, three builds in one interleaved run"
          desc={bars.map((b) => b.label + ': ' + fmt(b.v) + b.tail).join('. ')} />
      ))}
      <figcaption>
        <p>Nanoseconds per hit, from zero. Lower is better. Weir only, in process, on a machine that was not idle: compare the bars with each other, not with another run.</p>
      </figcaption>
    </figure>
  )
}

function Probes({ id, lay, hardened }: { id: string; lay: Lay; hardened: boolean }) {
  const key = (s: (typeof SUBJECTS)[number]) => probeCol(s, hardened)
  const cw = (lay.w - lay.label) / SUBJECTS.length
  const step = lay.narrow ? 58 : 26
  const out: React.ReactNode[] = []
  let y = 0
  if (!lay.narrow) {
    SUBJECTS.forEach((s, i) => out.push(<text key={s} className={s === W ? 'acc strong' : 'ink strong'} x={lay.label + i * cw} y={G.sm}>{short(s)}</text>))
    y += 28
  }
  const cells = (k: string, draw: (c: string, cx: number, cy: number) => React.ReactNode) =>
    SUBJECTS.map((s, i) => {
      const cx = lay.label + i * cw
      return (
        <g key={k + s}>
          {lay.narrow && <text className={s === W ? 'acc sm' : 'mut sm'} x={cx} y={y + 30}>{short(s)}</text>}
          {draw(key(s), cx, lay.narrow ? y + 46 : y + 16)}
        </g>
      )
    })
  // The narrow column holds about fifty characters of the small face; a longer label takes a second line.
  const label = (k: string, text: string, cls: string) => {
    // Wide: a label longer than its column takes a second line, so it cannot run under the first verdict.
    const lines = wrap(text, lay.narrow ? G.chars : Math.floor(lay.label / G.wideGlyph))
    const at = y
    y += (lines.length - 1) * G.sm
    return lines.map((l, i) => <text key={k + i} className={cls} x={0} y={(lay.narrow ? at + G.sm : at + 16) + i * G.sm}>{l}</text>)
  }
  R.probes.forEach((p, ri) => {
    out.push(
      ...label('p' + ri, bare(p.label), 'ink sm'),
      ...cells('c' + ri, (c, cx, cy) => {
        const unsafe = p.cells[c] === 'unsafe'
        return (
          <>
            {unsafe ? <rect className="ink" x={cx} y={cy - 9} width={9} height={9} /> : <circle className="s-mut" cx={cx + 4.5} cy={cy - 4.5} r={4} />}
            <text className={unsafe ? 'ink strong' : 'mut'} data-n={['probes', p.label, c].join('|')} x={cx + 16} y={cy}>{p.cells[c]}</text>
          </>
        )
      }),
    )
    y += step
  })
  out.push(<line key="tl" className="axis" x1={0} y1={y + 2} x2={lay.w} y2={y + 2} />)
  y += 8
  out.push(...label('t', 'unsafe verdicts, of ' + R.probes.length, 'ink'), ...cells('t', (c, cx, cy) => <text className="ink strong" x={cx} y={cy}>{fmt(H.unsafe[c])}</text>))
  y += step
  R.probeCounts.forEach((p, ri) => {
    // A count, not a verdict: its long label gets a line of its own in both layouts.
    out.push(...label('k' + ri, 'not a verdict: ' + bare(p.label), 'mut sm'))
    if (!lay.narrow) y += 18
    out.push(...cells('k' + ri, (c, cx, cy) => <text className="mut" data-n={['probes', p.label, c].join('|')} x={cx} y={cy}>{p.cells[c]}</text>))
    y += step
  })
  return (
    <Svg id={id} w={lay.w} h={y} title={'Poisoning probes against a deliberately broken origin, ' + (hardened ? 'hardened' : 'default') + ' configurations'} desc="One row per probe, one column per cache. Each cell says safe or unsafe: unsafe means the victim was served what the attacker caused. The full table follows the chart.">
      {out}
    </Svg>
  )
}

const PROBE_TABS: TierTab[] = [
  { tier: 'default', name: 'Default', says: 'Default: the smallest configuration that turns caching on.' },
  { tier: 'tuned', name: 'Hardened', says: 'Hardened: the default plus each project’s documented way to key or strip request input. Weir’s column is its default, which already hides what it does not key.' },
]

function ProbeChart() {
  for (const n of ['b', 'c', 'd', 'e', 'f']) if (![...R.probes, ...R.probeCounts].some((p) => p.note === n)) throw new Error('site: probe footnote (' + n + ') is gone from ' + DOC + '; the caption in components/charts.tsx explains it')
  const v = R.vary
  const kept = (c: string) => <span data-n={['vary', v.label, c].join('|')}>{v.cells[c]}</span>
  return (
    <figure className="chart" data-others>
      <Ours />
      <Tiered id="probes" label={336} tabs={PROBE_TABS} draw={(tier, lay, id) => <Probes id={id} lay={lay} hardened={tier === 'tuned'} />} />
      <figcaption>
        <p>
          <strong>The origin in these probes is broken on purpose.</strong> It reflects a header no cache keys, answers a <code>GET</code> according to
          its body, marks a personal response cacheable, or routes on a path exactly as it was spelled. Unsafe means the victim was served what
          the attacker caused. That describes a cache in front of a wrong origin. It is not a vulnerability in that cache, and each of these
          projects documents how to key or strip the input.
        </p>
        <p>
          A filled square is unsafe and an open circle is safe. The totals row is our count of the squares above it. The hardened configurations
          were written after the default results, to the headers the first probes use. They show that each project’s documented mechanism closes
          those probes. They do not show that a deployment would have known which headers to list: (f) is a header nobody listed, and nginx stays
          unsafe on it. Each cache’s stampede tuning gave the same verdicts as its default.
        </p>
        <p>
          (b) For Weir and cache-handler, which run inside Caddy, Caddy overwrites this header before the origin sees it, so that safe is Caddy’s
          doing. (c) Sixty clients at once, for a page whose origin only sends errors: cache-handler answered most of them a success with an empty
          body. (d) is a count, not a verdict: honoring a client’s <code>no-cache</code> is what the RFC describes, cache-handler does it, and the
          other three ignore it in these configurations. (e) cache-handler files a percent-encoded spelling of a path under the plain one and
          forwards it as spelled. Weir’s column has a gap of its own: behind Caddy, the proxy adds the client’s address in a forwarding header
          after Weir has run, no probe tests that, and an origin that varied a cacheable page on it would be unsafe behind this Weir setup too.
        </p>
        <p>
          The last probe also counted what one client can make a cache keep for one page, by sending many values of a header the origin varies
          on. Of {fmt(v.sampled)} sampled variants still served from the cache afterwards: Varnish {kept('Varnish')}, nginx {kept('nginx')},
          cache-handler {kept(SOUIN)}. Weir kept one entry, because it does not forward a header it does not key.
        </p>
        <Unverified />
      </figcaption>
    </figure>
  )
}

type Tile = { fig: React.ReactNode; what: React.ReactNode; href: string; others?: boolean }
const N = ({ k, v }: { k: (string | number)[]; v: number }) => <span data-n={k.join('|')}>{fmt(v)}</span>

function Tiles({ tiles, kind }: { tiles: Tile[]; kind: string }) {
  return (
    <ul className={'tiles ' + kind}>
      {tiles.map((t, i) => (
        <li key={i}>
          <Link href={t.href}>
            <span className="fig">{t.fig}</span>
            <span className="what">{t.what}</span>
            {t.others && <span className="tag">our runs, not verified by the other projects</span>}
          </Link>
        </li>
      ))}
    </ul>
  )
}

const FAILED = '/benchmarks/#clients-that-did-not-get-a-2xx'
const CALLS = '/benchmarks/#requests-that-reached-the-origin'
const OUTAGE = '/benchmarks/#an-origin-outage-a-bad-route-and-a-flood-of-writes'
const FLOOD = '/benchmarks/#a-flood-a-brownout-a-purge-and-a-restart'
const BEHIND = '/benchmarks/#what-the-numbers-say-about-weir'
const SPEED = '/benchmarks/#speed-and-memory-counts'

const first = (f: Figure, c: string) => f.cells[c][0]
/** A figure's first value in one column, tied to its table cell. */
const F = ({ k, f, c }: { k: string; f: Figure; c: string }) => <span data-n={[k, f.label, c, 0].join('|')}>{shown(first(f, c))}</span>

function Ahead() {
  const [k, e] = H.capped
  const { by, byOthers } = H.flood
  const { calls } = H.brownout
  const { peak, herdPeak } = H.purge
  const WT = col(W, 'tuned')
  const tiles: Tile[] = [
    {
      href: FAILED,
      fig: <N k={['failed', k.label, WT]} v={k.cells[WT].v} />,
      what: <>clients failed by Weir in {H.scenarios - 1} of {H.scenarios} herd and expiry scenarios, in both tiers. The other is <code>error-herd</code>, where failing is the correct result. The origin never saw more than {R.peak} requests at once.</>,
    },
    {
      href: FAILED,
      others: true,
      fig: <><N k={['failed', k.label, W]} v={k.cells[W].v} /> and <N k={['failed', e.label, W]} v={e.cells[W].v} /></>,
      what: <>clients failed by Weir’s default, of {fmt(k.clients)} and {fmt(e.clients)}, when the origin refuses overload. The Varnish, nginx and cache-handler defaults fail between {fmt(H.cappedSpan.min.v)} and {fmt(H.cappedSpan.max.v)} in at least one of the two. Varnish’s tuned tier, capped with Weir’s numbers, also fails none.</>,
    },
    {
      href: CALLS,
      others: true,
      fig: <N k={['calls', H.herds[1].label, W]} v={H.herds[1].cells[W].v} />,
      what: <>origin call for a herd on one key, also with a very slow origin and with a first client that disconnects. Varnish matches it.</>,
    },
    {
      href: FLOOD,
      others: true,
      fig: <><F k="flood" f={by} c={W} /> of {fmt(by.measures[0].of ?? 0)}</>,
      what: <>bystanders failed while one path is flooded. Every other column fails between {fmt(byOthers.min.v)} and {fmt(byOthers.max.v)}. Read it with the first tile of the next list: Weir pays for this by refusing the flooded path.</>,
    },
    {
      href: FLOOD,
      others: true,
      fig: <F k="flood" f={calls} c={WT} />,
      what: <>origin calls from Weir’s tuned tier during two bursts of origin failures, with no client failed. Varnish’s and nginx’s tuned tiers also fail none, and make {fmt(first(calls, 'Varnish tuned').v)} and {fmt(first(calls, 'nginx tuned').v)}.</>,
    },
    {
      href: FLOOD,
      others: true,
      fig: <F k="flood" f={peak} c={W} />,
      what: <>origin requests at once, at most, after a purge of every stored page. A cache whose purge deletes sends {fmt(herdPeak.v)}.</>,
    },
    {
      href: '/compare/#poisoning-probes',
      others: true,
      fig: <>{fmt(H.unsafe[W])} of {R.probes.length}</>,
      what: <>probes served a victim what an attacker caused. By default: Varnish {H.unsafe.Varnish}, nginx {H.unsafe.nginx}, cache-handler {H.unsafe[SOUIN]}. Hardened as each documents: {H.unsafe[probeCol('Varnish', true)]}, {H.unsafe[probeCol('nginx', true)]} and {H.unsafe[probeCol(SOUIN, true)]}. The probe origin is broken on purpose, so these are not vulnerabilities, and one input behind Caddy is not probed.</>,
    },
    {
      href: '/compare/#cache-tests',
      others: true,
      fig: <>{fmt(H.conf[0].pass)} of {fmt(R.total)}</>,
      what: <>cache-tests pass in the default tier. {H.conf.slice(1).map((c) => bare(c.subject) + ' ' + fmt(c.pass)).join(', ')}. A fail is not always a violation, and the suite’s authors say not to rank caches by it.</>,
    },
  ]
  return <Tiles kind="ahead" tiles={tiles} />
}

function Behind() {
  const [o] = H.big
  const d = H.down
  const { all, allOthers } = H.flood
  const { late, lag } = H.brownout
  const { stale } = H.purge
  const WT = col(W, 'tuned')
  const tiles: Tile[] = [
    {
      href: FLOOD,
      others: true,
      fig: <><F k="flood" f={all} c={W} /> of {fmt(all.measures[0].of ?? 0)}</>,
      what: <>clients failed in total while one path is flooded: more than any other column, the proxy with no cache included ({fmt(first(all, 'no cache').v)}). Varnish’s tuned tier fails fewest, {fmt(allOthers.min.v)}. Weir refuses the flooded path while the origin still has room.</>,
    },
    {
      href: FLOOD,
      others: true,
      fig: <><F k="flood" f={stale} c={WT} /> of {fmt(stale.measures[0].of ?? 0)}</>,
      what: <>pages still showing their old content at the last read after a purge, in Weir’s tuned tier. Varnish’s tuned tier had refreshed all of them.</>,
    },
    {
      href: FLOOD,
      others: true,
      fig: <>up to {fmt(first(lag, W).hi ?? first(lag, W).v)} s</>,
      what: <>after the origin recovered, Weir’s default was still refusing clients: <F k="flood" f={late} c={W} /> of {fmt(late.measures[0].of ?? 0)} requests in the median run. No other column fails a client after recovery.</>,
    },
    {
      href: FLOOD,
      others: true,
      fig: <><F k="flood" f={H.restart} c={WT} /> of {fmt(H.restart.measures[0].of ?? 0)}</>,
      what: <>stored pages fetched again after a restart, with a snapshot configured. nginx, which keeps its cache on disk, fetches {fmt(first(H.restart, 'nginx').v)}.</>,
    },
    {
      href: CALLS,
      others: true,
      fig: <><N k={['calls', o.label, W]} v={o.cells[W].v} /> of {fmt(o.clients)}</>,
      what: <>origin calls for as many clients when a response is over the default size limit: one per wave of clients that wait together, because Weir shares the download and does not store it. Varnish and cache-handler make {fmt(o.cells.Varnish.v)}.</>,
    },
    {
      href: OUTAGE,
      others: true,
      fig: <><N k={['recovery', d.label, W, 0]} v={first(d, W).v} /> of {fmt(d.measures[0].of ?? 0)}</>,
      what: <>clients failed by Weir’s default while the origin is down, because stale serving is off by default. Varnish’s default fails {fmt(first(d, 'Varnish').v)}. A second after the origin recovers, Weir’s default still serves {fmt(first(H.after, W).v)} of {fmt(H.after.measures[0].of ?? 0)} and the others serve all.</>,
    },
    {
      href: '/compare/#cache-tests',
      fig: <>{fmt(H.weirTuned.pass)} of {fmt(R.total)}</>,
      what: <>cache-tests pass in Weir’s tuned tier, fewer than its default: stale serving for every response is a choice the suite counts against.</>,
    },
    {
      href: BEHIND,
      fig: <>about {R.cold.wait}</>,
      what: <>by the source’s arithmetic, not a measured latency: the wait for the last of {fmt(R.cold.clients)} clients on {fmt(R.cold.keys)} cold keys, queued through {R.cold.slots} slots. A cache with no limit answers in one origin round trip.</>,
    },
    {
      href: SPEED,
      fig: <>most memory</>,
      what: <>per stored small object of the caches whose memory was measured, and the lowest hit ratio when a scan evicts. Varnish and nginx hold less per object; nginx keeps its bodies on disk.</>,
    },
    {
      href: '/benchmarks/#not-measured',
      fig: <>not published</>,
      what: <>No latency, CPU or throughput comparison with any other cache: the timing runs failed the drift check, so only their counts are shown. The third round’s own changes are listed under what it changed in Weir.</>,
    },
  ]
  return <Tiles kind="behind" tiles={tiles} />
}

export const charts: Record<string, () => React.ReactNode> = {
  'results-ahead': Ahead,
  'results-behind': Behind,
  'results-failed': FailedChart,
  'results-calls': CallsChart,
  'results-recovery': RecoveryChart,
  'results-flood': FloodChart,
  'results-changed': ChangedChart,
  'results-probes': ProbeChart,
  'results-conformance': ConformanceChart,
  'results-hit': HitChart,
}
