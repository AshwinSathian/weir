import Link from 'next/link'
import { Transcript } from '@/components/client'
import { Hydrograph } from '@/components/diagrams'
import { MODULE, REPO, SHOWN_VERSION } from '@/data/release'
import { transcript } from '@/data/transcript'
import { render } from '@/lib/md'
import { readmeMain } from '@/lib/repo'
import { modelCounts } from '@/lib/tables'

const GATE = `${REPO}/actions/runs/38259811052`

const caddyfile = `example.com {
	weir {
		name site-a
	}
	reverse_proxy app:8080
}`

const failures: [string, string, React.ReactNode][] = [
  ['Synchronized expiry', 'Entries stored together expire together, and the origin gets the whole batch back at once.',
    <>Jitter shortens each lifetime by up to 10%, and a hot entry is refreshed in the background shortly before it expires. <Link href="/docs/concepts/freshness-and-staleness/">Freshness</Link></>],
  ['Hot-key stampede', 'One popular entry expires and every request waiting on it asks the origin.',
    <>Requests for one key share one fetch. Followers wait at most 10 s, and a leader slower than 10 s stops collecting new followers. <Link href="/docs/concepts/coalescing/">Coalescing</Link></>],
  ['Cross-key stampede', 'A purge, a restart or a cold start makes thousands of different keys miss together. Coalescing cannot help, because no two requests share a key.',
    <>Every fetch holds one of 64 slots, 16 per path unless slots are idle. 8,192 more wait up to 5 s; the rest get stale content or a 503 with <code>Retry-After</code>. <Link href="/docs/concepts/origin-protection/">Origin protection</Link></>],
  ['Origin or storage outage', 'The origin slows down, and every request pays the full timeout before failing.',
    <>Stale-while-revalidate and stale-if-error serve what is stored, a circuit breaker stops calling a failing origin, and a 2 s negative entry answers repeats of a failure. <Link href="/docs/concepts/origin-protection/">Origin protection</Link></>],
  ['Poisoning and cache busting', 'A header the key ignores changes the stored response for everyone, or random query strings turn every request into a miss.',
    <>The origin receives only keyed, allowed and protocol headers, so there is no unkeyed input to poison with. A miss-rate detector flags a path whose requests have stopped hitting. <Link href="/docs/concepts/cache-key/">The cache key</Link></>],
]

const not: [string, string][] = [
  ['A CDN', 'One process, one logical origin per engine.'],
  ['A WAF or rate limiter', 'It bounds origin load. It does not decide which clients deserve service, so put a rate limiter in front for floods of distinct paths.'],
  ['A private cache', 'It is always a shared cache in the RFC 9111 sense.'],
  ['A cache for application data', 'It caches HTTP responses. For function results use singleflight or sturdyc.'],
  ['A Varnish replacement with VCL', 'Configuration is a Go struct or a Caddyfile block. There is no scripting language.'],
  ['A store for 206 responses', 'It answers single byte ranges from complete stored objects.'],
  ['A prefix purge', 'Purge by URL, by RFC 9875 group, or everything.'],
  ['A multi-node memory cache', 'Each node has its own memory store. Several nodes share a cache through the Valkey store.'],
]

export default async function Home() {
  const [go, caddy] = await Promise.all([
    render('```go\n' + readmeMain() + '\n```'),
    render('```text\n' + caddyfile + '\n```'),
  ])
  return (
    <>
      <div className="wrap">
        <section className="hero">
          <div>
            <div className="label">Go library · net/http and Caddy · Apache-2.0</div>
            <h1>Shared HTTP caching in front of an origin that has to stay up.</h1>
            <p className="lede">
              Weir forwards the request it keyed, so a header the cache ignores cannot poison an entry. And a stampede
              cannot reach the origin faster than a budget you set.
            </p>
            <div className="actions">
              <Link className="btn primary" href="/docs/quickstart/">Quickstart</Link>
              <a className="btn" href={REPO}>GitHub</a>
            </div>
            <div className="cmd">
              <span className="prompt">$</span>
              <code>go get {MODULE}@{SHOWN_VERSION}</code>
            </div>
          </div>
          <div>
            <Transcript lines={transcript} />
            <p className="term-caption">
              Real output. The second request never left the process. Its <code>ttl</code> is 43 and not 47 because
              jitter took four seconds off this entry, so entries stored together do not expire together.
            </p>
          </div>
        </section>
      </div>

      <section className="band">
        <div className="wrap">
          <div className="label">What a weir does</div>
          <h2>Ten thousand entries expire within seconds of each other. The origin sees 32 requests at a time.</h2>
          <p className="intro">
            A weir is a low dam that regulates flow without stopping it. Every origin fetch holds a slot from a limiter,
            whatever caused it: a miss, a refresh, a purge, a cold start.
          </p>
          <figure className="hydro">
            <Hydrograph />
            <figcaption>
              The shape is a schematic. The numbers are measured: the synchronized-expiry scenario of <code>make load</code>{' '}
              on an Apple M4 Pro with Go 1.27.1, where the busiest second saw 1,485 origin calls.{' '}
              <Link href="/benchmarks/">Method and limits</Link>
            </figcaption>
          </figure>
        </div>
      </section>

      <section className="band">
        <div className="wrap">
          <div className="label">Failure modes</div>
          <h2>Five ways a cache takes its origin down</h2>
          <p className="intro">Each technique exists elsewhere. Weir puts them in one Go library with a written threat model and a test for each.</p>
          <ol className="ledger">
            {failures.map(([name, problem, answer], i) => (
              <li key={name}>
                <span className="n">0{i + 1}</span>
                <div>
                  <h3>{name}</h3>
                  <p>{problem}</p>
                </div>
                <p><strong>What Weir does.</strong> {answer}</p>
              </li>
            ))}
          </ol>
        </div>
      </section>

      <section className="band">
        <div className="wrap">
          <div className="label">Try it</div>
          <h2>A main function, or a Caddyfile block</h2>
          <p className="intro">The zero <code>weir.Config</code> is valid, and <code>name</code> is the only setting the Caddy directive requires.</p>
          <div className="split prose">
            <div>
              <h3>Go, with net/http</h3>
              <div dangerouslySetInnerHTML={{ __html: go.html }} />
            </div>
            <div>
              <h3>Caddy</h3>
              <div dangerouslySetInnerHTML={{ __html: caddy.html }} />
              <p>
                Build Caddy with the module first. The <Link href="/docs/quickstart/">quickstart</Link> has the{' '}
                <code>xcaddy</code> line and the output to expect at each step.
              </p>
            </div>
          </div>
        </div>
      </section>

      <section className="band">
        <div className="wrap">
          <div className="label">Non-goals</div>
          <h2>What Weir is not</h2>
          <ul className="notlist">
            {not.map(([what, why]) => (
              <li key={what}><b>{what}.</b> <span>{why}</span></li>
            ))}
          </ul>
          <p style={{ marginTop: 'var(--s5)' }}>
            If one of these is what you need, the <Link href="/compare/">comparison page</Link> says which project to use instead.
          </p>
        </div>
      </section>

      <section className="band">
        <div className="wrap">
          <div className="label">Evidence</div>
          <h2>What was checked before the release</h2>
          <div className="evidence">
            <Link href="/conformance/">
              <span className="fig">284 / 365</span>
              <span className="what">cache-tests pass. Each of the other 81 has a written reason.</span>
            </Link>
            <Link href="/security/threats/">
              <span className="fig">{modelCounts().threats}</span>
              <span className="what">threats in the model, each with an answer and a test.</span>
            </Link>
            <a href={GATE}>
              <span className="fig">29 × 60 min</span>
              <span className="what">fuzz targets, one hour each, and the failure-mode tests 100 times under the race detector.</span>
            </a>
            <a href={`${REPO}/blob/main/deps_test.go`}>
              <span className="fig">0</span>
              <span className="what">third-party imports in the root module. A test enforces it.</span>
            </a>
          </div>
        </div>
      </section>
    </>
  )
}
