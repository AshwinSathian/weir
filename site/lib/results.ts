// Chart data for the benchmarks and compare pages, parsed from docs/benchmarks.md.
// No number is typed here: every value comes out of a table cell or a sentence of
// that document, and a table whose header changed throws, so the build fails
// instead of drawing a chart from the wrong column.
//
// This file imports nothing, so scripts/check.mjs can load it with Node's type
// stripping and run the same parser the pages use.

export const DOC = 'docs/benchmarks.md'

/** The four caches the charts compare, as the tables' column headers name their default tier. */
export const SUBJECTS = ['Weir v1.2.0', 'Varnish', 'nginx', 'cache-handler (Souin)'] as const
export type Subject = (typeof SUBJECTS)[number]
export type Tier = 'default' | 'tuned'
const WEIR = SUBJECTS[0]
/** cache-handler, Caddy's cache module built on Souin. */
export const SOUIN = SUBJECTS[3]
/** The column of a subject's tier. Weir's tuned column drops the version. */
export const col = (s: Subject, t: Tier) => (t === 'default' ? s : s === WEIR ? 'Weir tuned' : `${s} tuned`)
/** The probe table's column for a subject, as configured by default or hardened. Weir has one: its default is the strict one. */
export const probeCol = (s: Subject, hardened: boolean) => (hardened && s !== WEIR ? `${s} hardened` : s)

const NINE = ['no cache', ...SUBJECTS.flatMap((s) => [col(s, 'default'), col(s, 'tuned')])]

export const HEAD = {
  calls: ['Scenario', 'clients', ...NINE],
  failed: ['Scenario', 'clients', ...NINE],
  recovery: ['Figure', ...NINE],
  flood: ['Figure', ...NINE],
  changed: ['Figure', 'v1.1.0', 'v1.2.0'],
  probes: ['Probe', ...SUBJECTS.flatMap((s) => (s === WEIR ? [s] : [s, `${s} hardened`]))],
  vary: ['', WEIR, 'Varnish', 'nginx', SOUIN, `${SOUIN} tuned`, 'no cache'],
  conformance: ['Subject', 'Tier', 'Pass', 'Assertion failed', 'Setup failed'],
  hit: ['Benchmark', '`main` (v1.0.1)', 'hardening branch', 'v1.1 hit path', 'change from `main`'],
}

/** Where each table lives: the start of its heading line. scripts/check.mjs reads the same map. */
export const SECTION = {
  calls: '### Comparison: origin calls',
  failed: '### Comparison: clients that did not get a 2xx',
  recovery: '### Comparison: failure and recovery scenarios',
  flood: '### Comparison: flood, brownout, purge and restart',
  says: '### Comparison: what the numbers say about Weir',
  changed: '### Comparison: what the round changed in Weir',
  probes: '### Comparison: security probes',
  vary: '### Comparison: security probes',
  conformance: '### Comparison: conformance',
  hit: '### v1.1 hardening (2026-10-13)',
}
const ROUND = '## Comparison with other caches, round 3 ('

const bad = (m: string): never => {
  throw new Error(`site: ${DOC}: ${m}`)
}

function section(md: string, heading: string): string {
  const lines = md.split('\n')
  const start = lines.findIndex((l) => l.startsWith(heading))
  if (start < 0) bad(`no heading starting with "${heading}"`)
  const level = /^#+/.exec(heading)![0].length
  const rest = lines.slice(start + 1)
  const end = rest.findIndex((l) => {
    const m = /^(#+) /.exec(l)
    return m !== null && m[1].length <= level
  })
  return rest.slice(0, end < 0 ? undefined : end).join('\n')
}

type Raw = Record<string, string>

/** Which table of its section a key reads, counting from 0. Every other key reads the first. */
export const NTH: Partial<Record<keyof typeof HEAD, number>> = { vary: 1 }

/** One table of a section, one record per row, keyed by header. The header must equal `head`. */
function table(md: string, key: keyof typeof HEAD): Raw[] {
  const head = HEAD[key]
  const lines = section(md, SECTION[key]).split('\n')
  const cells = (l: string) => l.trim().slice(1, -1).split('|').map((c) => c.trim())
  const starts = lines.flatMap((l, i) => (l.startsWith('|') && !lines[i - 1]?.startsWith('|') ? [i] : []))
  const at = starts[NTH[key] ?? 0] ?? -1
  if (at < 0) bad(`"${SECTION[key]}" has no table ${NTH[key] ?? 0}`)
  const got = cells(lines[at])
  if (got.join('|') !== head.join('|')) bad(`the table under "${SECTION[key]}" changed shape: expected columns [${head.join(', ')}], found [${got.join(', ')}]`)
  const rows: Raw[] = []
  for (let i = at + 2; i < lines.length && lines[i].startsWith('|'); i++) {
    const c = cells(lines[i])
    if (c.length !== head.length) bad(`a row under "${SECTION[key]}" has ${c.length} cells, the header has ${head.length}: ${lines[i]}`)
    rows.push(Object.fromEntries(head.map((h, j) => [h, c[j]])))
  }
  if (!rows.length) bad(`the table under "${SECTION[key]}" is empty`)
  return rows
}

/** "1 004.5", "2,000" and "872" are numbers; anything else is a changed cell format. */
export function num(s: string): number {
  const t = s.replace(/[\s ,]/g, '')
  if (!/^\d+(\.\d+)?$/.test(t)) bad(`"${s}" is not a number`)
  return Number(t)
}

/** The words the tables use for a subject that cannot run a scenario. It is never a zero. */
export const NA = 'not available'

/** A count: the median of the repetitions, with their range where they differed by more than a tenth. */
export type Cell = { v: number; lo?: number; hi?: number; na?: true }
/** "4,550 (3,113 to 5,000)" is 4,550 with a range; "not available" is no number at all. */
export function cell(s: string): Cell {
  if (s === NA) return { v: 0, na: true }
  const m = /^(.*?)(?: \(([\d.,]+) to ([\d.,]+)\))?$/.exec(s)!
  return { v: num(m[1]), ...(m[2] ? { lo: num(m[2]), hi: num(m[3]) } : {}) }
}

const bare = (s: string) => s.replace(/`/g, '')

export type Scenario = { label: string; name: string; clients: number; cells: Record<string, Cell> }
function scenarios(md: string, key: 'calls' | 'failed'): Scenario[] {
  return table(md, key).map((r) => ({
    label: r.Scenario,
    name: bare(r.Scenario),
    clients: num(r.clients),
    cells: Object.fromEntries(NINE.map((c) => [c, cell(r[c])])),
  }))
}

/** One counted thing in a figure label: "clients failed of 100" has a total, "origin calls" has none. */
export type Measure = { raw: string; of?: number; served: boolean }
function measures(text: string, n: number): Measure[] {
  // One value in the cells: the whole label is one measure, commas and all ("200s of 200, 10 s after recovery").
  const parts = n === 1 ? [text] : text.split(', ')
  return parts.map((p) => {
    const of = /\bof ([\d,]+)/.exec(p)
    // "200s of 200" and "requests answered 2xx, of 300,000" count what went right: the bar's filled part is the rest.
    return { raw: p, ...(of ? { of: num(of[1]) } : {}), served: /^\d+s\b|answered \dxx/.test(p) }
  })
}

export type Figure = { label: string; scenario: string; measures: Measure[]; cells: Record<string, Cell[]> }
function figures(md: string, key: 'recovery' | 'flood' | 'changed'): Figure[] {
  return table(md, key).map((r) => {
    const m = /^`([\w-]+)`: (.+)$/.exec(r.Figure)
    if (!m) bad(`figure label "${r.Figure}" is not "\`scenario\`: what"`)
    const ms = measures(m![2], r[HEAD[key][1]].split(', ').length)
    const cells = Object.fromEntries(
      HEAD[key].slice(1).map((c) => {
        const vs = r[c].split(', ').map(cell)
        if (vs.length !== ms.length) bad(`"${r.Figure}" names ${ms.length} measures, column "${c}" has ${vs.length} values`)
        return [c, vs]
      }),
    )
    return { label: r.Figure, scenario: m![1], measures: ms, cells }
  })
}

export type Probe = { label: string; text: string; note?: string; cells: Record<string, string> }

export type Results = ReturnType<typeof parseResults>

export function parseResults(md: string) {
  const date = /^(\d{4}-\d{2}-\d{2})\)/.exec(md.slice(md.indexOf(ROUND) + ROUND.length))?.[1] ?? bad(`no "${ROUND}date)" heading`)

  const calls = scenarios(md, 'calls')
  const failed = scenarios(md, 'failed')
  if (calls.map((s) => `${s.name} ${s.clients}`).join() !== failed.map((s) => `${s.name} ${s.clients}`).join()) bad('the origin-calls and failed-clients tables list different scenarios')

  const recovery = figures(md, 'recovery')
  const flood = figures(md, 'flood')
  const changed = figures(md, 'changed')

  const probeRows = table(md, 'probes').map((r): Probe => {
    const m = /^(.*?)(?: \(([a-z])\))?$/.exec(r.Probe)!
    return { label: r.Probe, text: m[1], ...(m[2] ? { note: m[2] } : {}), cells: Object.fromEntries(HEAD.probes.slice(1).map((c) => [c, r[c]])) }
  })
  const isVerdict = (p: Probe) => Object.values(p.cells).every((v) => v === 'safe' || v === 'unsafe')
  const probes = probeRows.filter(isVerdict)
  // Rows that are counts, not verdicts (footnote d). They must still be numbers.
  const probeCounts = probeRows.filter((p) => !isVerdict(p))
  for (const p of probeCounts) Object.values(p.cells).forEach(num)
  if (!/^The tuned tier of every subject gave the same verdicts as its default\./m.test(section(md, SECTION.probes))) bad('the probes section no longer says the tuned tier gave the same verdicts as the default, and the grid shows no tuned column')

  // What one client can make a cache hold for one URL: the second table of the probes section.
  const varyRows = table(md, 'vary')
  const varyRow = (re: RegExp) => varyRows.find((r) => re.test(r[''])) ?? bad(`no row matching ${re} in the second table under "${SECTION.vary}"`)
  const kept = varyRow(/still served from the cache/)
  const sampled = num(/^of (\d+) sampled variants/.exec(kept[''])?.[1] ?? bad(`"${kept['']}" does not start with "of N sampled variants"`))
  const vary = { label: kept[''], sampled, cells: Object.fromEntries(HEAD.vary.slice(1).map((c) => [c, kept[c]])) }

  const conformance = table(md, 'conformance').map((r) => ({ subject: r.Subject, tier: r.Tier, row: `${r.Subject}, ${r.Tier}`, pass: num(r.Pass), assertion: num(r['Assertion failed']), setup: num(r['Setup failed']) }))
  const total = conformance[0].pass + conformance[0].assertion + conformance[0].setup
  if (conformance.some((c) => c.pass + c.assertion + c.setup !== total)) bad('conformance rows do not add up to one total')

  const hitRows = table(md, 'hit')
  const hitRow = (name: string) => hitRows.find((r) => r.Benchmark === name) ?? bad(`no "${name}" row under "${SECTION.hit}"`)
  const builds = HEAD.hit.slice(1, 4)
  const ns = hitRow('`BenchmarkServeHitSmall`')
  const al = hitRow('hit, bytes and allocations')
  const hit = builds.map((b) => {
    const t = /^([\d.]+) ns/.exec(ns[b]) ?? bad(`"${ns[b]}" is not nanoseconds`)
    const a = /, (\d+)$/.exec(al[b]) ?? bad(`"${al[b]}" is not "bytes, allocations"`)
    return { build: b, ns: num(t[1]), allocs: num(a[1]) }
  })

  // Sentences the pages quote. A reworded source fails the build, so the copy cannot outlive it.
  const says = section(md, SECTION.says)
  const sentence = (text: string, re: RegExp, what: string) => re.exec(text) ?? bad(`cannot find ${what}`)
  const cold = sentence(says, /([\d,]+) clients on ([\d,]+) cold keys are all served, but through (\d+) slots, so the last ones wait about (\d+ s)/, 'the cold-start sentence')
  const peak = sentence(says, /the origin never sees more than (\d+) requests at once/, 'the origin peak sentence')

  return {
    date,
    calls,
    failed,
    recovery,
    flood,
    changed,
    probes,
    probeCounts,
    vary,
    conformance,
    total,
    hit,
    cold: { clients: num(cold[1]), keys: num(cold[2]), slots: num(cold[3]), wait: cold[4] },
    peak: num(peak[1]),
  }
}

const want = (ok: boolean, claim: string) => {
  if (!ok) bad(`the page says "${claim}" and the tables no longer do. Change the copy in components/charts.tsx with the data`)
}

/**
 * The numbers behind the headline tiles. Each claim the tiles make in words is
 * checked against the tables here, so a sentence cannot survive a rerun that
 * contradicts it.
 */
export function headlines(r: Results) {
  const others = SUBJECTS.slice(1)
  const tiers: Tier[] = ['default', 'tuned']
  const everyOther = ['no cache', ...others.flatMap((o) => tiers.map((t) => col(o, t)))]
  const row = (set: Scenario[], name: string) => set.find((s) => s.name === name) ?? bad(`no scenario "${name}"`)
  const fig = (set: Figure[], re: RegExp) => set.find((f) => re.test(f.label)) ?? bad(`no figure matching ${re}`)
  const rec = (re: RegExp) => fig(r.recovery, re)
  const fl = (re: RegExp) => fig(r.flood, re)
  const W = WEIR
  const one = (f: Figure, c: string) => f.cells[c][0]
  const span = (cs: Cell[]) => ({ min: cs.reduce((a, b) => (b.v < a.v ? b : a)), max: cs.reduce((a, b) => (b.v > a.v ? b : a)) })

  // Weir fails no client, in either tier, except where failing is the right answer.
  const honest = r.failed.filter((s) => tiers.some((t) => s.cells[col(W, t)].v !== 0))
  want(honest.length === 1 && honest[0].name === 'error-herd', 'Weir fails a client only in error-herd')
  want(honest[0].cells[W].v === honest[0].clients && honest[0].cells['no cache'].v === honest[0].clients, 'in error-herd every client gets the origin error, with or without a cache')
  for (const re of [/bystanders failed/, /stored entries lost/]) want(tiers.every((t) => one(rec(re), col(W, t)).v === 0), `Weir: 0 for ${re.source}`)

  // The two scenarios with a capped origin.
  const capped = r.failed.filter((s) => s.name.endsWith('-capped'))
  want(capped.length === 2 && capped.every((s) => s.cells[W].v === 0), "Weir's default serves everyone when the origin is capped")
  const cappedOthers = others.flatMap((o) => {
    const hit = capped.map((s) => s.cells[o]).filter((c) => c.v > 0)
    want(hit.length > 0, `the ${o} default fails clients in at least one capped scenario`)
    return hit
  })
  want(capped.every((s) => s.cells['Varnish tuned'].v === 0), "Varnish's tuned tier fails nobody in either capped scenario")

  // One origin call for a herd on one key.
  const herds = ['cold-herd', 'slow-origin-herd', 'leader-abort'].map((n) => row(r.calls, n))
  for (const s of [W, 'Varnish'] as const) want(herds.every((h) => tiers.every((t) => h.cells[col(s, t)].v === 1)), `${s} makes one origin call for each single-key herd`)

  // A flood of one path: every bystander served, and what that costs.
  const by = fl(/busting-flood.*bystanders failed/)
  const all = fl(/busting-flood.*all clients failed/)
  want(tiers.every((t) => one(by, col(W, t)).v === 0), 'Weir fails no bystander of a flooded path')
  const byOthers = span(everyOther.map((c) => one(by, c)))
  want(byOthers.min.v > 0, 'every other column fails bystanders of a flooded path')
  const allOthers = span(everyOther.map((c) => one(all, c)))
  want(tiers.every((t) => one(all, col(W, t)).v > allOthers.max.v), 'Weir fails more clients in total than any other column in the flood')
  want(one(all, 'Varnish tuned').v === allOthers.min.v, "Varnish's tuned tier fails the fewest clients in the flood")

  // A brownout: stale serving hides it; without it the breaker keeps refusing after recovery.
  const burst = fl(/brownout.*failed during the bursts/)
  const late = fl(/brownout.*failed outside the bursts/)
  const lag = fl(/brownout.*seconds from recovery/)
  const calls = fl(/brownout.*origin calls during the bursts/)
  for (const s of [W, 'Varnish', 'nginx'] as const) want(one(burst, col(s, 'tuned')).v === 0 && one(late, col(s, 'tuned')).v === 0, `${s}'s tuned tier fails nobody in the brownout`)
  want(one(late, W).v > 0 && everyOther.every((c) => one(late, c).v === 0), "only Weir's default fails clients after the origin has recovered")
  want(['Varnish tuned', 'nginx tuned'].every((c) => one(calls, c).v > one(calls, col(W, 'tuned')).v), "Weir's tuned tier makes fewer origin calls in the bursts than Varnish's or nginx's")

  // A purge.
  const peak = fl(/purge-herd.*most origin requests at once/)
  const stale = fl(/purge-herd.*not refreshed at the last read/)
  const purged = everyOther.filter((c) => !one(peak, c).na)
  want(purged.length > 0 && tiers.every((t) => !one(peak, col(W, t)).na), 'Weir and at least one other cache can purge')
  const herdPeak = span(purged.map((c) => one(peak, c))).max
  want(tiers.every((t) => one(peak, col(W, t)).v < herdPeak.v), 'a purge reaches the origin as a smaller burst through Weir than through a cache that deletes')
  want(one(stale, col(W, 'tuned')).v > 0 && one(stale, 'Varnish tuned').v === 0 && one(stale, W).v === 0, "only Weir's tuned tier leaves purged content unrefreshed")

  // A restart.
  const lost = fl(/restart.*origin calls on replay/)
  want(tiers.every((t) => one(lost, col(W, t)).v === lost.measures[0].of) && tiers.every((t) => one(lost, col('nginx', t)).v === 0), 'a restart costs Weir one origin call per stored URL and nginx none')

  const unsafe = (c: string) => r.probes.filter((p) => p.cells[c] === 'unsafe').length
  want(unsafe(W) === 0, 'Weir is safe on every probe')
  for (const o of others) want(unsafe(probeCol(o, true)) <= unsafe(o), `hardening does not make ${o} less safe`)

  // Losses the first round already showed.
  const big = ['oversize-herd', 'large-body-herd'].map((n) => row(r.calls, n))
  for (const o of ['Varnish', SOUIN] as const) want(big.every((s) => s.cells[o].v === 1), `${o} fetches an over-size response once`)
  // oversize-herd sends its clients in waves; Weir shares one download per wave and stores none.
  want(tiers.every((t) => big[0].cells[col(W, t)].v > 1 && big[0].cells[col(W, t)].v < big[0].clients), 'Weir fetches an over-size response once per wave of clients, not once per client')
  want(tiers.every((t) => big[1].cells[col(W, t)].v === 1), 'Weir fetches a response over the size limit once for one wave of clients')
  const down = rec(/clients failed while down/)
  want(one(down, W).v === down.measures[0].of, "Weir's default fails every client while the origin is down")
  want(one(down, 'Varnish').v < one(down, W).v, "Varnish's default fails fewer clients than Weir's during the outage")
  const after = rec(/1 s after recovery/)
  want(one(after, W).v === 0 && others.every((o) => one(after, o).v === after.measures[0].of), "Weir's default serves nobody a second after recovery and the other defaults serve everyone")

  // Conformance: each subject's default row, Weir first.
  const conf = r.conformance.filter((c) => c.tier === 'default')
  const weirConf = r.conformance.filter((c) => c.subject.startsWith('Weir'))
  want(conf[0]?.subject.startsWith('Weir') && conf.length === SUBJECTS.length, 'the conformance table has one default row per cache, Weir first')
  const weirTuned = weirConf.find((c) => c.tier === 'tuned') ?? bad('no tuned conformance row for Weir')
  want(weirTuned.pass < conf[0].pass, "Weir's tuned tier passes fewer cache-tests than its default")

  return {
    scenarios: r.failed.length,
    capped,
    cappedSpan: span(cappedOthers),
    herds,
    flood: { by, all, byOthers, allOthers },
    brownout: { late, lag, calls },
    purge: { peak, stale, herdPeak },
    restart: lost,
    unsafe: Object.fromEntries(HEAD.probes.slice(1).map((c) => [c, unsafe(c)])) as Record<string, number>,
    big,
    down,
    after,
    conf,
    weirTuned,
  }
}
