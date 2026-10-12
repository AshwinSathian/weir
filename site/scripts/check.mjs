// Source checks that need no build. `node scripts/check.mjs` runs all but the
// release check; `node scripts/check.mjs release` runs only that one.
//   style     no em dashes or banned words; at most one bold phrase per section
//   numbers   every measured figure in hand-written copy is in content/claims.md,
//             and every claims row whose source is a repo file appears in that file
//   data      data/benchmarks.json figures appear in docs/benchmarks.md
//   counts    threat, risk and invariant numbers in the copy equal docs/06; every residual risk has a
//             plain-language row on the security page; cache-tests numbers equal their source
//   versions  every install line in the repository's documents names one version
//   snippet   the README program compiles against this tree
//   release   with RELEASED=true, all four modules resolve at VERSION from the Go proxy
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const SITE = path.resolve(import.meta.dirname, '..')
const ROOT = path.resolve(SITE, '..')
const read = (p) => fs.readFileSync(p, 'utf8')
const errors = []
const fail = (m) => errors.push(m)

const walk = (dir, ext) =>
  fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? walk(path.join(dir, e.name), ext) : e.name.endsWith(ext) ? [path.join(dir, e.name)] : [],
  )
const rel = (p) => path.relative(ROOT, p)

// Hand-written copy: site content, the home page, and the repo's front-door files.
const prose = [
  ...walk(path.join(SITE, 'content'), '.md'),
  path.join(SITE, 'app/page.tsx'),
  ...['README.md', 'CONTRIBUTING.md', 'SECURITY.md'].map((f) => path.join(ROOT, f)).filter(fs.existsSync),
]

function style() {
  const banned = /\b(robust|seamless(ly)?|leverag(e|es|ed|ing)|comprehensive|delv(e|es|ed|ing))\b/i
  for (const f of prose) {
    const text = read(f)
    text.split('\n').forEach((line, i) => {
      if (line.includes('—')) fail(`${rel(f)}:${i + 1}: em dash`)
      const m = banned.exec(line)
      if (m) fail(`${rel(f)}:${i + 1}: banned word "${m[0]}"`)
    })
    if (!f.endsWith('.md')) continue
    // Sections are split at headings; fenced code does not count.
    const body = text.replace(/```[\s\S]*?```/g, '')
    for (const sec of body.split(/^#{1,6} /m)) {
      const bold = sec.match(/\*\*[^*]+\*\*/g) ?? []
      if (bold.length > 1) fail(`${rel(f)}: section "${sec.split('\n')[0].slice(0, 40)}" has ${bold.length} bold phrases (limit 1)`)
    }
  }
}

function numbers() {
  const register = read(path.join(SITE, 'content/claims.md'))
  const rows = register.split('\n').filter((l) => l.startsWith('| `'))
  const figures = new Set()
  for (const row of rows) {
    const [figure, source] = row.slice(1).split('|').map((c) => c.trim())
    const fig = figure.replace(/`/g, '')
    figures.add(fig)
    const file = /^`([^`]+)`/.exec(source)?.[1]
    if (!file) continue // an external source: a URL checked by hand, with a date
    const src = read(path.join(ROOT, file)).replace(/(\d)[  ](?=\d{3}\b)/g, '$1,') // "52 975" in a table is 52,975
    const num = /[\d.,]*\d/.exec(fig)?.[0] ?? fig // the unit may be spelled differently in the source
    if (!src.includes(num) && !src.includes(num.replace(/,/g, ''))) fail(`content/claims.md: "${fig}" is not in ${file}`)
  }
  // A measured figure: a number with a time or allocation unit, or a count of four digits or more.
  const measured = /\b\d+(?:\.\d+)?\s?(?:ns|µs|ms)\b|\b\d{1,3}(?:,\d{3})+\b|\b\d+ allocations\b/g
  for (const f of prose.filter((p) => p !== path.join(SITE, 'content/claims.md'))) {
    const text = read(f).replace(/```[\s\S]*?```/g, '')
    for (const m of text.match(measured) ?? []) {
      if (!figures.has(m)) fail(`${rel(f)}: "${m}" is not in site/content/claims.md`)
    }
  }
}

function data() {
  const bench = JSON.parse(read(path.join(SITE, 'data/benchmarks.json')))
  const src = read(path.join(ROOT, bench.source))
  for (const row of bench.hitPath.rows) {
    if (!src.includes(row.ns)) fail(`data/benchmarks.json: ${row.ns} is not in ${bench.source}`)
  }
}

function counts() {
  const tm = read(path.join(ROOT, 'docs/06-threat-model.md'))
  const ids = (re) => [...new Set([...tm.matchAll(re)].map((m) => m[1]))]
  const risks = ids(/^- (R-\d+)\./gm)
  const actual = { threats: ids(/^\| (T-\d+) \|/gm).length, 'residual risks': risks.length, invariants: ids(/^- (INV-\d+) /gm).length }
  for (const f of prose) {
    const text = read(f)
    for (const [word, n] of Object.entries(actual)) {
      for (const m of text.matchAll(new RegExp(`\\b(\\d+) ${word}\\b`, 'g'))) {
        if (Number(m[1]) !== n) fail(`${rel(f)}: says "${m[0]}", docs/06 has ${n}`)
      }
    }
  }
  const page = read(path.join(SITE, 'content/pages/security/index.md'))
  for (const r of risks) if (!page.includes(`| ${r} |`)) fail(`site/content/pages/security/index.md: no plain-language row for ${r}`)
  const ct = /(\d+) tests, (\d+) pass, (\d+) fail/.exec(read(path.join(ROOT, 'docs/cache-tests-expected-failures.md')))
  if (!ct) return fail('docs/cache-tests-expected-failures.md: no "N tests, N pass, N fail" line')
  for (const f of prose) {
    const text = read(f)
    for (const m of text.matchAll(/\b(\d+) of (?:the )?(\d+) (?:cache-tests|tests)\b/g)) {
      if (m[2] !== ct[1] || (m[1] !== ct[2] && m[1] !== ct[3])) fail(`${rel(f)}: says "${m[0]}", the baseline is ${ct[1]} tests, ${ct[2]} pass, ${ct[3]} fail`)
    }
  }
}

function versions() {
  const files = ['README.md', 'docs/runbook.md', 'docs/08-caddy-adapter-spec.md', 'caddy/doc.go', 'examples/weirproxy/README.md']
  const seen = new Map()
  for (const f of files) {
    for (const m of read(path.join(ROOT, f)).matchAll(/AshwinSathian\/weir[\w/]*@(v\d+\.\d+\.\d+(?:-rc\.\d+)?)/g)) {
      seen.set(m[1], [...(seen.get(m[1]) ?? []), f])
    }
  }
  if (seen.size !== 1) return fail(`install lines name ${seen.size} versions: ${[...seen].map(([v, fs]) => `${v} in ${[...new Set(fs)].join(', ')}`).join('; ')}`)
  const docs = [...seen.keys()][0]
  const site = /VERSION = '([^']+)'/.exec(read(path.join(SITE, 'data/release.ts')))[1]
  if (site !== docs) console.log(`note versions: documents say ${docs}, the site shows ${site} until site/data/release.ts is bumped after the tags exist`)
}

function snippet() {
  const m = /```go\n([\s\S]*?)\n```/.exec(read(path.join(ROOT, 'README.md')))
  if (!m) return fail('README.md: no go block')
  const dir = path.join(ROOT, 'examples/.readme-check')
  fs.mkdirSync(dir, { recursive: true })
  try {
    fs.writeFileSync(path.join(dir, 'main.go'), m[1] + '\n')
    execFileSync('go', ['vet', './examples/.readme-check'], { cwd: ROOT, stdio: 'pipe' })
  } catch (e) {
    fail(`README.md: the Go program does not compile:\n${e.stderr ?? e}`)
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
}

function release() {
  const src = read(path.join(SITE, 'data/release.ts'))
  if (!/RELEASED = true/.test(src)) return console.log('release: RELEASED is false, nothing to resolve')
  const version = /VERSION = '([^']+)'/.exec(src)[1]
  const mod = /MODULE = '([^']+)'/.exec(src)[1]
  const tmp = fs.mkdtempSync(path.join(fs.realpathSync(process.env.RUNNER_TEMP ?? '/tmp'), 'weir-release-'))
  for (const sub of ['', '/observe/prom', '/store/valkey', '/caddy']) {
    try {
      execFileSync('go', ['list', '-m', `${mod}${sub}@${version}`], { cwd: tmp, stdio: 'pipe', env: { ...process.env, GOFLAGS: '-mod=mod', GOWORK: 'off' } })
      console.log(`release: ${mod}${sub}@${version} resolves`)
    } catch {
      fail(`release: ${mod}${sub}@${version} does not resolve from the module proxy, but the site says it is released`)
    }
  }
}

const only = process.argv[2]
const checks = only === 'release' ? { release } : { style, numbers, counts, versions, data, snippet }
for (const [name, fn] of Object.entries(checks)) {
  const before = errors.length
  fn()
  console.log(`${errors.length === before ? 'ok  ' : 'FAIL'} ${name}`)
}
if (errors.length) {
  console.error('\n' + errors.join('\n'))
  process.exit(1)
}
