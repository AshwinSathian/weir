// Charts for the benchmarks and compare pages. Static inline SVG in the
// vocabulary of diagrams.tsx. Every number drawn here comes from
// lib/results.ts, which parses docs/benchmarks.md: this file types none, and
// scripts/check.mjs fails on a digit in its copy. Each chart is drawn twice,
// for a wide and a narrow column, and CSS shows one, so a phone gets text at
// full size instead of a scaled-down picture.

import Link from 'next/link'
import { repoFile } from '@/lib/repo'
import { DOC, HEAD, SUBJECTS, col, headlines, parseResults, type Cell, type Figure, type Tier } from '@/lib/results'
import { Svg } from './diagrams'

const R = parseResults(repoFile(DOC))
const H = headlines(R)
const W = SUBJECTS[0]
const fmt = (n: number) => n.toLocaleString('en-US')
const bare = (s: string) => s.replaceAll(String.fromCharCode(96), '') // code spans in table labels
const marked = (c: Cell) => (c.note ? fmt(c.v) + ' (' + c.note + ')' : fmt(c.v))

type Lay = { narrow: boolean; w: number; label: number }
const G = { wide: 688, slim: 326, num: 44, bar: 10, sm: 14, chars: 50 }
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
const HATCH = ['M', 0, 6, 'L', 6, 0].join(' ')

type Val = { v: number; hatch?: boolean; flag?: string }
type Col = { key: string; title: string; own: boolean }
type Row = {
  n: (c: string) => string
  name: string
  what: string
  right?: string
  of?: number
  mode: 'bar' | 'outline' | 'plain'
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
    const cellTop = lay.narrow ? top + 34 : top
    // Wide: the total shares the second line with the description, so a long name cannot run into it.
    const second = !lay.narrow && r.right ? r.right + ', ' + r.what : r.what
    out.push(
      <text key={'n' + ri} className="ink" x={0} y={top + G.sm}>{r.name}</text>,
      <text key={'w' + ri} className="mut sm" x={0} y={top + 29}>{second}</text>,
      lay.narrow && r.right && <text key={'r' + ri} className="mut sm" x={lay.w} y={top + G.sm} textAnchor="end">{r.right}</text>,
    )
    cols.forEach((c, i) => {
      const cx = lay.label + i * cw
      const { v, hatch, flag } = r.get(c.key)
      const tx = lay.narrow ? cx : cx + G.num + 6
      const tw = lay.narrow ? cw - 10 : cw - G.num - 20
      const ty = lay.narrow ? cellTop + 34 : cellTop + 5
      const len = r.of && v ? Math.max(2, Math.min(1, v / r.of) * tw) : 0
      const who = c.own ? 'weir' : 'other'
      out.push(
        <g key={ri + c.key}>
          {lay.narrow && <text className={c.own ? 'acc sm' : 'mut sm'} x={cx} y={cellTop + 12}>{c.title}</text>}
          <text className={v || r.mode !== 'bar' ? 'ink' : 'mut'} data-n={r.n(c.key)} x={lay.narrow ? cx : cx + G.num} y={lay.narrow ? cellTop + 28 : cellTop + G.sm} textAnchor={lay.narrow ? 'start' : 'end'}>{fmt(v)}</text>
          {r.of ? <rect className="track" x={tx} y={ty} width={tw} height={G.bar} rx={2} /> : null}
          {len ? <rect className={(r.mode === 'outline' ? 'o-' : 'b-') + who} fill={hatch ? 'url(#' + id + '-h)' : undefined} x={tx} y={ty} width={len} height={G.bar} rx={2} /> : null}
          {flag && <text className="ink sm" x={lay.narrow ? tx : cx + 8} y={ty + G.bar + 13}>{flag}</text>}
        </g>,
      )
    })
    y = (lay.narrow ? cellTop + 46 : top + 34) + (flags ? G.sm : 0)
    if (r.note) {
      out.push(<text key={'o' + ri} className="ink sm" x={0} y={y + 8}>{r.note}</text>)
      y += 18
    }
    y += 10
  })
  return (
    <Svg id={id} w={lay.w} h={y} title={title} desc={desc}>
      <defs>
        <pattern id={id + '-h'} width={6} height={6} patternUnits="userSpaceOnUse">
          <path className="hatch" d={HATCH} />
        </pattern>
      </defs>
      {out}
    </Svg>
  )
}

const both = (label: number, draw: (lay: Lay, suffix: string) => React.ReactNode) =>
  lays(label).map((lay) => (
    <div key={String(lay.narrow)} className={lay.narrow ? 'only-slim' : 'only-wide'}>{draw(lay, lay.narrow ? '-s' : '-w')}</div>
  ))

const TIERS: { tier: Tier; name: string; says: string }[] = [
  { tier: 'default', name: 'Default', says: 'Default tier: the smallest configuration that turns caching on.' },
  { tier: 'tuned', name: 'Tuned', says: 'Tuned tier: each project’s documented stampede and stale settings.' },
]

/** Both tiers are in the page. Two radio buttons and CSS show one; with no CSS, or on paper, both show. */
function Tiered({ id, label, draw }: { id: string; label: number; draw: (tier: Tier, lay: Lay, id: string) => React.ReactNode }) {
  return (
    <div className="tiers">
      {TIERS.map((t, i) => <input key={t.tier} className="sr-only" type="radio" name={id} id={id + '-' + t.tier} defaultChecked={!i} />)}
      <div className="toggle">
        <span className="label">Configuration</span>
        {TIERS.map((t) => <label key={t.tier} htmlFor={id + '-' + t.tier}>{t.name}</label>)}
      </div>
      {TIERS.map((t) => (
        <div key={t.tier} className={'tier ' + t.tier}>
          <p className="tier-name">{t.says}</p>
          {both(label, (lay, s) => draw(t.tier, lay, id + '-' + t.tier + s))}
        </div>
      ))}
    </div>
  )
}

const tierCols = (tier: Tier): Col[] => SUBJECTS.map((s) => ({ key: col(s, tier), title: s, own: s === W }))

/** R4a of the comparison spec: the statement travels with every figure that shows another project. */
function Unverified() {
  return (
    <p className="unverified">
      One round on one machine, {R.date}. These are our own runs with configurations we wrote. They have not been verified by the owners of
      Varnish, nginx or cache-handler.
    </p>
  )
}

const NoteA = () => <p>{R.noteA}</p>

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
      mode: kind === 'failed' && meta.expected ? 'outline' : 'bar',
      note: meta.expected ? EXPECTED : undefined,
      get: (c) => {
        const cell = s.cells[c]
        if (kind === 'failed') return { v: cell.v, hatch: Boolean(cell.note), flag: cell.note && '(' + cell.note + ') too low' }
        const f = lost.cells[c]
        return { v: cell.v, flag: f.v && !meta.expected ? '✕ ' + marked(f) : undefined }
      },
    }
  })
}

function FailedChart() {
  const rows = scenarioRows('failed')
  return (
    <figure className="chart" data-others>
      <Tiered id="failed" label={224} draw={(tier, lay, id) => (
        <Grid id={id} lay={lay} cols={tierCols(tier)} rows={rows} flags title={'Clients that did not get a 2xx, ' + tier + ' tier'}
          desc="One row per scenario, one column per cache. Each cell is the count of clients that failed and a bar for its share of the scenario's clients. The full table follows the chart." />
      )} />
      <figcaption>
        <p>
          A bar is the share of that scenario’s clients that failed, and the number is the count. Any count above zero draws at least a sliver. Lower is
          better, with one exception: in <code>error-herd</code> the origin only sends errors, so every client failing is the correct result. Those
          bars are outlined, not filled. A refusal that a cache sends to protect the origin counts as a failure, for Weir as for the others.
        </p>
        <NoteA />
        <Unverified />
      </figcaption>
    </figure>
  )
}

function CallsChart() {
  const rows = scenarioRows('calls')
  return (
    <figure className="chart" data-others>
      <Tiered id="calls" label={224} draw={(tier, lay, id) => (
        <Grid id={id} lay={lay} cols={tierCols(tier)} rows={rows} flags title={'Requests that reached the origin, ' + tier + ' tier'}
          desc="One row per scenario, one column per cache. Each cell is the count of origin calls and a bar for its share of the scenario's clients, with a mark where clients failed in the same run. The full table follows the chart." />
      )} />
      <figcaption>
        <p>
          A bar is origin calls as a share of that scenario’s clients, and it stops at one call per client. Fewer calls is better only when nobody
          failed. A ✕ gives the clients that failed in the same run, so a short bar with a ✕ is an origin that refused, not a cache that saved work.
          Calls are read the moment a wave ends: a cache that answers from a stale copy has made only part of its background refreshes by then.
        </p>
        <NoteA />
        <Unverified />
      </figcaption>
    </figure>
  )
}

const figureRows = (key: 'recovery' | 'changed', set: Figure[]): Row[] =>
  set.flatMap((f) =>
    f.measures.map((m, i): Row => ({
      n: (c) => [key, f.label, c, i].join('|'),
      name: f.scenario,
      what: m.raw,
      of: m.of,
      mode: m.served ? 'outline' : m.of ? 'bar' : 'plain',
      get: (c) => ({ v: f.cells[c][i] }),
    })),
  )

function RecoveryChart() {
  const rows = figureRows('recovery', R.recovery)
  return (
    <figure className="chart" data-others>
      <Tiered id="recovery" label={220} draw={(tier, lay, id) => (
        <Grid id={id} lay={lay} cols={tierCols(tier)} rows={rows} flags={false} title={'Failure and recovery scenarios, ' + tier + ' tier'}
          desc="One row per figure, one column per cache: bystanders failed beside a failing route, clients failed and origin calls during an origin outage, clients served after it, and stored entries lost to a flood. The full table follows the chart." />
      )} />
      <figcaption>
        <p>
          Filled bars count clients that failed or entries that were lost: lower is better. Outlined bars count clients served after the origin came
          back: higher is better. Origin calls while down has no fixed total, so it is a number with no bar.
        </p>
        <Unverified />
      </figcaption>
    </figure>
  )
}

function ChangedChart() {
  const [, before, after] = HEAD.changed.slice(1)
  const late = R.changed.filter((f) => String(f.cells[before]) !== String(f.cells[after])).length
  const rows = figureRows('changed', R.changed)
  return (
    <figure className="chart">
      {both(250, (lay, s) => (
        <Grid id={'changed' + s} lay={lay} rows={rows} flags={false} title="What the round changed in Weir"
          cols={HEAD.changed.slice(1).map((c) => ({ key: c, title: lay.narrow ? c.replace(' before ', ' pre-') : c, own: true }))}
          desc="One row per figure, three columns: the last release, the hardening branch before this round's fixes, and the code released after them. The full table follows the chart." />
      ))}
      <figcaption>
        <p>
          Weir only. Filled bars count failures: lower is better. The outlined row counts clients served: higher is better. {late} of the{' '}
          {R.changed.length} figures were still wrong on the hardening branch. This round found them and they were fixed after it, which is
          the middle column. The scenarios were not changed to fit the fixes.
        </p>
        <p>One round on one machine, {R.date}.</p>
      </figcaption>
    </figure>
  )
}

type Bar = { n: string; label: string; v: number; tail: string; own: boolean }

/** Horizontal bars from zero, each under its label. */
function Bars({ id, title, desc, bars, max, lay }: { id: string; title: string; desc: string; bars: Bar[]; max: number; lay: Lay }) {
  const tw = lay.narrow ? lay.w : lay.w - 230
  const step = lay.narrow ? 62 : 46
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
          </g>
        )
      })}
    </Svg>
  )
}

function ConformanceChart() {
  const bars = R.conformance.map((c): Bar => ({ n: ['conformance', c.label, 'Pass'].join('|'), label: bare(c.label), v: c.pass, tail: ' of ' + fmt(R.total) + ' pass', own: c.label.startsWith('Weir') }))
  return (
    <figure className="chart" data-others>
      {both(0, (lay, s) => (
        <Bars id={'conf' + s} lay={lay} bars={bars} max={R.total} title="cache-tests passed, each cache in its default tier"
          desc={bars.map((b) => b.label + ': ' + fmt(b.v) + b.tail).join('. ')} />
      ))}
      <figcaption>
        <p>
          The bars start at zero and end at the whole suite. The suite mixes required and optional behavior, and a fail is every test that did not
          pass, including tests whose setup failed. So a fail is not always a violation. Only Weir’s fails are analyzed, on
          the <Link href="/conformance/">conformance page</Link>. The suite’s authors say its results should not be used to compare feature support.
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

function Probes({ id, lay }: { id: string; lay: Lay }) {
  const cw = (lay.w - lay.label) / SUBJECTS.length
  const step = lay.narrow ? 58 : 26
  const out: React.ReactNode[] = []
  let y = 0
  if (!lay.narrow) {
    SUBJECTS.forEach((s, i) => out.push(<text key={s} className={s === W ? 'acc big' : 'ink big'} x={lay.label + i * cw} y={G.sm}>{s}</text>))
    y += 28
  }
  const cells = (key: string, draw: (s: string, cx: number, cy: number) => React.ReactNode) =>
    SUBJECTS.map((s, i) => {
      const cx = lay.label + i * cw
      return (
        <g key={key + s}>
          {lay.narrow && <text className={s === W ? 'acc sm' : 'mut sm'} x={cx} y={y + 30}>{s}</text>}
          {draw(s, cx, lay.narrow ? y + 46 : y + 16)}
        </g>
      )
    })
  // The narrow column holds about fifty characters of the small face; a longer label takes a second line.
  const label = (key: string, text: string, cls: string) => {
    const lines = wrap(text, lay.narrow ? G.chars : text.length)
    const at = y
    y += (lines.length - 1) * G.sm
    return lines.map((l, i) => <text key={key + i} className={cls} x={0} y={(lay.narrow ? at + G.sm : at + 16) + i * G.sm}>{l}</text>)
  }
  R.probes.forEach((p, ri) => {
    out.push(
      ...label('p' + ri, bare(p.label), 'ink sm'),
      ...cells('c' + ri, (s, cx, cy) => {
        const unsafe = p.cells[s] === 'unsafe'
        return (
          <>
            {unsafe ? <rect className="ink" x={cx} y={cy - 9} width={9} height={9} /> : <circle className="s-mut" cx={cx + 4.5} cy={cy - 4.5} r={4} />}
            <text className={unsafe ? 'ink strong' : 'mut'} data-n={['probes', p.label, s].join('|')} x={cx + 16} y={cy}>{p.cells[s]}</text>
          </>
        )
      }),
    )
    y += step
  })
  out.push(<line key="tl" className="axis" x1={0} y1={y + 2} x2={lay.w} y2={y + 2} />)
  y += 8
  out.push(...label('t', 'unsafe verdicts, of ' + R.probes.length, 'ink'), ...cells('t', (s, cx, cy) => <text className="ink strong" x={cx} y={cy}>{fmt(H.unsafe[s as keyof typeof H.unsafe])}</text>))
  y += step
  R.probeCounts.forEach((p, ri) => {
    // A count, not a verdict: its long label gets a line of its own in both layouts.
    out.push(...label('k' + ri, 'not a verdict: ' + bare(p.label), 'mut sm'))
    if (!lay.narrow) y += 18
    out.push(...cells('k' + ri, (s, cx, cy) => <text className="mut" data-n={['probes', p.label, s].join('|')} x={cx} y={cy}>{p.cells[s]}</text>))
    y += step
  })
  return (
    <Svg id={id} w={lay.w} h={y} title="Poisoning probes against a deliberately broken origin" desc="One row per probe, one column per cache. Each cell says safe or unsafe: unsafe means the victim was served what the attacker caused. The full table follows the chart.">
      {out}
    </Svg>
  )
}

function ProbeChart() {
  for (const n of ['b', 'd']) if (![...R.probes, ...R.probeCounts].some((p) => p.note === n)) throw new Error('site: probe footnote (' + n + ') is gone from ' + DOC + '; the caption in components/charts.tsx explains it')
  return (
    <figure className="chart" data-others>
      {both(336, (lay, s) => <Probes id={'probes' + s} lay={lay} />)}
      <figcaption>
        <p>
          <strong>The origin in these probes is broken on purpose.</strong> It reflects a header no cache keys, answers a <code>GET</code> according to
          its body, or marks a personal response cacheable. Unsafe means the victim was served what the attacker caused. That describes a cache in
          front of a wrong origin. It is not a vulnerability in that cache, and each of these projects documents how to key or strip the input.
        </p>
        <p>
          A filled square is unsafe and an open circle is safe. Both tiers of every cache gave the same verdicts, and the tuned tier is stampede
          tuning, not a hardened configuration. (b) For Weir and cache-handler, which run inside Caddy, Caddy overwrites this header before the origin
          sees it, so that safe is Caddy’s doing. (d) is a count, not a verdict: honoring a client’s <code>no-cache</code> is what the RFC describes,
          cache-handler does it, and the other three ignore it in these configurations.
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
      {tiles.map((t) => (
        <li key={t.href + String(t.what).length}>
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
const OUTAGE = '/benchmarks/#an-origin-outage-a-bad-route-and-a-flood'
const BEHIND = '/benchmarks/#what-the-numbers-say-about-weir'

function Ahead() {
  const [k, e] = H.capped
  const [lo, hi] = [H.cappedMin, H.cappedMax]
  const last = R.hit[R.hit.length - 1]
  const tiles: Tile[] = [
    {
      href: FAILED,
      fig: <N k={['failed', k.label, col(W, 'tuned')]} v={k.cells[col(W, 'tuned')].v} />,
      what: <>clients failed by {W} in {H.scenarios - 1} of {H.scenarios} herd and expiry scenarios, in both tiers. The other is <code>error-herd</code>, where failing is the correct result. The origin never saw more than {R.peak} requests at once.</>,
    },
    {
      href: FAILED,
      others: true,
      fig: <><N k={['failed', k.label, W]} v={k.cells[W].v} /> and <N k={['failed', e.label, W]} v={e.cells[W].v} /></>,
      what: <>clients failed by Weir’s default, of {fmt(k.clients)} and {fmt(e.clients)}, when the origin refuses overload. The Varnish, nginx and cache-handler defaults fail between {marked(lo)} and {marked(hi)} in at least one of the two. (a) marks a cell that undercounts.</>,
    },
    {
      href: CALLS,
      others: true,
      fig: <N k={['calls', H.herds[1].label, W]} v={H.herds[1].cells[W].v} />,
      what: <>origin call for a herd on one key, also with a very slow origin and with a first client that disconnects. Varnish matches it.</>,
    },
    {
      href: '/compare/#poisoning-probes',
      others: true,
      fig: <>{fmt(H.unsafe[W])} of {R.probes.length}</>,
      what: <>probes served a victim what an attacker caused. Varnish {H.unsafe.Varnish}, nginx {H.unsafe.nginx}, cache-handler {H.unsafe.Souin}. The probe origin is broken on purpose, so these are not vulnerabilities.</>,
    },
    {
      href: '/compare/#cache-tests',
      others: true,
      fig: <>{fmt(R.conformance[0].pass)} of {fmt(R.total)}</>,
      what: <>cache-tests pass. {R.conformance.slice(1).map((c) => bare(c.label) + ' ' + fmt(c.pass)).join(', ')}. A fail is not always a violation.</>,
    },
    {
      href: '/benchmarks/#one-cached-hit',
      fig: <><N k={['hit', '`BenchmarkServeHitSmall`', last.build]} v={last.ns} /> ns</>,
      what: <>and {last.allocs} allocations for one cached hit, against {fmt(R.hit[0].ns)} ns and {R.hit[0].allocs} for the release before it in the same run.</>,
    },
  ]
  return <Tiles kind="ahead" tiles={tiles} />
}

function Behind() {
  const [o, l] = H.big
  const d = H.down
  const tiles: Tile[] = [
    {
      href: CALLS,
      others: true,
      fig: <><N k={['calls', o.label, W]} v={o.cells[W].v} /> and <N k={['calls', l.label, W]} v={l.cells[W].v} /></>,
      what: <>origin calls for as many clients when a response is over the default size limit: one per client. Varnish and cache-handler make {fmt(o.cells.Varnish.v)}.</>,
    },
    {
      href: OUTAGE,
      others: true,
      fig: <><N k={['recovery', d.label, W, 0]} v={d.cells[W][0]} /> of {fmt(d.measures[0].of ?? 0)}</>,
      what: <>clients failed by Weir’s default while the origin is down, because stale serving is off by default. Varnish’s default fails {fmt(d.cells.Varnish[0])}.</>,
    },
    {
      href: BEHIND,
      fig: <>about {R.cold.wait}</>,
      what: <>of waiting for the last of {fmt(R.cold.clients)} clients on {fmt(R.cold.keys)} cold keys, queued through {R.cold.slots} slots. A cache with no limit answers in one origin round trip.</>,
    },
    {
      href: '/benchmarks/#not-measured',
      fig: <>not run</>,
      what: <>No speed, memory or hit-ratio comparison with any other cache exists, and no latency from this round is published.</>,
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
  'results-changed': ChangedChart,
  'results-probes': ProbeChart,
  'results-conformance': ConformanceChart,
  'results-hit': HitChart,
}
