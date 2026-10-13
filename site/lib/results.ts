// Chart data for the benchmarks and compare pages, parsed from docs/benchmarks.md.
// No number is typed here: every value comes out of a table cell or a sentence of
// that document, and a table whose header changed throws, so the build fails
// instead of drawing a chart from the wrong column.
//
// This file imports nothing, so scripts/check.mjs can load it with Node's type
// stripping and run the same parser the pages use.

export const DOC = 'docs/benchmarks.md'

/** The four caches the charts compare, as the tables' column headers name their default tier. */
export const SUBJECTS = ['Weir v1.1', 'Varnish', 'nginx', 'Souin'] as const
export type Subject = (typeof SUBJECTS)[number]
export type Tier = 'default' | 'tuned'
export const col = (s: Subject, t: Tier) => (t === 'tuned' ? `${s} tuned` : s)

const TEN = ['no cache', 'Weir v1.0.1', ...SUBJECTS.flatMap((s) => [s, `${s} tuned`])]

export const HEAD = {
  calls: ['Scenario', 'clients', ...TEN],
  failed: ['Scenario', 'clients', ...TEN],
  recovery: ['Figure', ...TEN],
  changed: ['Figure', 'v1.0.1', 'v1.1 before HRD-21', 'v1.1'],
  probes: ['Probe', 'Weir v1.0.1', 'Weir v1.1', 'Varnish', 'nginx', 'Souin'],
  conformance: ['Subject', 'Pass', 'Fail'],
  hit: ['Benchmark', '`main` (v1.0.1)', 'after HRD-01 to 15', 'after HRD-19', 'change from `main`'],
}

/** Where each table lives: the start of its heading line. scripts/check.mjs reads the same map. */
export const SECTION = {
  calls: '### Comparison: origin calls',
  failed: '### Comparison: clients that did not get a 2xx',
  recovery: '### Comparison: failure and recovery scenarios',
  says: '### Comparison: what the numbers say about Weir',
  changed: '### Comparison: what the round changed in Weir',
  probes: '### Comparison: security probes',
  conformance: '### Comparison: conformance',
  hit: '### v1.1 hardening (2026-10-13)',
}
const ROUND = '## Comparison with other caches, round 1 ('

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

/** The first table of a section, one record per row, keyed by header. The header must equal `head`. */
function table(md: string, key: keyof typeof HEAD): Raw[] {
  const head = HEAD[key]
  const lines = section(md, SECTION[key]).split('\n')
  const cells = (l: string) => l.trim().slice(1, -1).split('|').map((c) => c.trim())
  const at = lines.findIndex((l) => l.startsWith('|'))
  if (at < 0) bad(`"${SECTION[key]}" has no table`)
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

export type Cell = { v: number; note?: string }
/** "872 (a)" is 872 with footnote a. */
function cell(s: string): Cell {
  const m = /^(.*?)(?: \(([a-z])\))?$/.exec(s)!
  return { v: num(m[1]), ...(m[2] ? { note: m[2] } : {}) }
}

const bare = (s: string) => s.replace(/`/g, '')

export type Scenario = { label: string; name: string; clients: number; cells: Record<string, Cell> }
function scenarios(md: string, key: 'calls' | 'failed'): Scenario[] {
  return table(md, key).map((r) => ({
    label: r.Scenario,
    name: bare(r.Scenario),
    clients: num(r.clients),
    cells: Object.fromEntries(TEN.map((c) => [c, cell(r[c])])),
  }))
}

/** One counted thing in a figure label: "clients failed of 100" has a total, "origin calls" has none. */
export type Measure = { raw: string; of?: number; served: boolean }
function measures(text: string, n: number): Measure[] {
  // One value in the cells: the whole label is one measure, commas and all ("200s of 200, 10 s after recovery").
  const parts = n === 1 ? [text] : text.split(', ')
  return parts.map((p) => {
    const of = /\bof ([\d,]+)/.exec(p)
    return { raw: p, ...(of ? { of: num(of[1]) } : {}), served: /^\d+s\b/.test(p) }
  })
}

export type Figure = { label: string; scenario: string; measures: Measure[]; cells: Record<string, number[]> }
function figures(md: string, key: 'recovery' | 'changed'): Figure[] {
  return table(md, key).map((r) => {
    const m = /^`([\w-]+)`: (.+)$/.exec(r.Figure)
    if (!m) bad(`figure label "${r.Figure}" is not "\`scenario\`: what"`)
    const ms = measures(m![2], r[HEAD[key][1]].split(', ').length)
    const cells = Object.fromEntries(
      HEAD[key].slice(1).map((c) => {
        const vs = r[c].split(', ').map(num)
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
  for (const p of probes) if (p.cells['Weir v1.0.1'] !== p.cells['Weir v1.1']) bad(`probe "${p.label}": the two Weir versions differ, and the grid shows one Weir column`)
  if (!/^Both tiers of every subject gave the same verdicts\.$/m.test(section(md, SECTION.probes))) bad('the probes section no longer says both tiers gave the same verdicts, and the grid shows one column per cache')

  const conformance = table(md, 'conformance').map((r) => ({ label: r.Subject, pass: num(r.Pass), fail: num(r.Fail) }))
  const total = conformance[0].pass + conformance[0].fail
  if (conformance.some((c) => c.pass + c.fail !== total)) bad('conformance rows do not add up to one total')

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
  const failedNotes = section(md, SECTION.failed)
  const sentence = (text: string, re: RegExp, what: string) => re.exec(text) ?? bad(`cannot find ${what}`)
  const noteA = sentence(failedNotes, /^- (\(a\) [^.]+\.)[^\n]*?(The harness counts status codes only, so these cells undercount)/m, 'footnote (a) under the failed-clients table')
  const cold = sentence(says, /([\d,]+) clients on ([\d,]+) cold keys are all served, but through (\d+) slots, so the last ones wait about (\d+ s)/, 'the cold-start sentence')
  const peak = sentence(says, /the origin never sees more than (\d+) requests at once/, 'the origin peak sentence')

  return {
    date,
    calls,
    failed,
    recovery,
    changed,
    probes,
    probeCounts,
    conformance,
    total,
    hit,
    noteA: `${noteA[1]} ${noteA[2]}.`,
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
  const row = (set: Scenario[], name: string) => set.find((s) => s.name === name) ?? bad(`no scenario "${name}"`)
  const rec = (re: RegExp) => r.recovery.find((f) => re.test(f.label)) ?? bad(`no recovery figure matching ${re}`)
  const W = SUBJECTS[0]

  // Weir fails no client, in either tier, except where failing is the right answer.
  const honest = r.failed.filter((s) => tiers.some((t) => s.cells[col(W, t)].v !== 0))
  want(honest.length === 1 && honest[0].name === 'error-herd', 'Weir fails a client only in error-herd')
  want(honest[0].cells[W].v === honest[0].clients && honest[0].cells['no cache'].v === honest[0].clients, 'in error-herd every client gets the origin error, with or without a cache')
  for (const re of [/bystanders failed/, /stored entries lost/]) want(tiers.every((t) => rec(re).cells[col(W, t)][0] === 0), `Weir: 0 for ${re.source}`)

  // The two scenarios with a capped origin.
  const capped = r.failed.filter((s) => s.name.endsWith('-capped'))
  want(capped.length === 2 && capped.every((s) => s.cells[W].v === 0), "Weir's default serves everyone when the origin is capped")
  const cappedOthers = others.flatMap((o) => {
    const hit = capped.map((s) => s.cells[o]).filter((c) => c.v > 0)
    want(hit.length > 0, `the ${o} default fails clients in at least one capped scenario`)
    return hit
  })
  const by = (a: Cell, b: Cell) => a.v - b.v
  cappedOthers.sort(by)

  // One origin call for a herd on one key.
  const herds = ['cold-herd', 'slow-origin-herd', 'leader-abort'].map((n) => row(r.calls, n))
  for (const s of [W, 'Varnish'] as const) want(herds.every((h) => tiers.every((t) => h.cells[col(s, t)].v === 1)), `${s} makes one origin call for each single-key herd`)

  const unsafe = (c: string) => r.probes.filter((p) => p.cells[c] === 'unsafe').length
  want(unsafe(W) === 0, 'Weir is safe on every probe')

  // Losses.
  const big = ['oversize-herd', 'large-body-herd'].map((n) => row(r.calls, n))
  for (const o of ['Varnish', 'Souin'] as const) want(big.every((s) => s.cells[o].v === 1), `${o} fetches an over-size response once`)
  want(big.every((s) => tiers.every((t) => s.cells[col(W, t)].v === s.clients)), 'Weir fetches an over-size response once per client')
  const down = rec(/clients failed while down/)
  want(down.cells[W][0] === down.measures[0].of, "Weir's default fails every client while the origin is down")
  want(down.cells.Varnish[0] < down.cells[W][0], "Varnish's default fails fewer clients than Weir's during the outage")

  return {
    scenarios: r.failed.length,
    capped,
    cappedMin: cappedOthers[0],
    cappedMax: cappedOthers[cappedOthers.length - 1],
    herds,
    unsafe: Object.fromEntries(SUBJECTS.map((s) => [s, unsafe(s)])) as Record<Subject, number>,
    big,
    down,
  }
}
