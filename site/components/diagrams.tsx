// Hand-drawn inline SVG. Colors come from CSS classes in site.css (.dg), so
// every diagram follows the theme. Each has a title and a description, and
// the page text beside it says the same thing in words.

import bench from '@/data/benchmarks.json'

type P = { children?: React.ReactNode; title: string; desc: string; w: number; h: number; id: string }

export function Svg({ children, title, desc, w, h, id }: P) {
  return (
    <svg className="dg" viewBox={`0 0 ${w} ${h}`} role="img" aria-labelledby={`${id}-t ${id}-d`}>
      <title id={`${id}-t`}>{title}</title>
      <desc id={`${id}-d`}>{desc}</desc>
      {children}
    </svg>
  )
}

/**
 * The home page figure. The shape is a schematic; the labelled numbers are
 * from the synchronized-expiry load scenario in docs/benchmarks.md.
 */
export function Hydrograph({ slim = false }: { slim?: boolean }) {
  if (slim) {
    // A phone gets the same picture at a third of the width, so it keeps the
    // shape and drops all but the two numbers that matter, at a legible size.
    return (
      <Svg id="hydro-s" w={360} h={230} title="Demand against origin load during a synchronized expiry" desc="Demand rises to a sharp peak as 10,000 entries expire within seconds; origin fetches in flight stay flat at the limit of 32.">
        <line className="axis" x1="8" y1="180" x2="352" y2="180" />
        <path className="f-water" d="M8 180 L8 168 C50 166 70 160 85 136 C98 112 102 52 116 50 C131 48 136 106 150 128 C161 144 172 148 186 148 L186 180 Z" />
        <path className="ink" d="M186 124 h8 v56 h-8 z" />
        <path className="f-acc" d="M194 150 H352 V180 H194 Z" />
        <text className="ink big" x="8" y="22">10,000 keys expire</text>
        <text className="ink big" x="200" y="120">origin: 32 at once</text>
        <text className="mut" x="8" y="206">all refetched, 0 client errors</text>
      </Svg>
    )
  }
  return (
    <Svg id="hydro" w={760} h={250} title="Demand against origin load during a synchronized expiry" desc="On the left, demand rises to a sharp peak as 10,000 entries expire within seconds. On the right of the weir, origin fetches in flight stay flat at the limit of 32.">
      <line className="axis" x1="24" y1="212" x2="736" y2="212" />
      {/* demand: pool behind the weir */}
      <path className="f-water" d="M24 212 L24 196 C100 194 140 190 170 160 C195 132 205 66 232 62 C262 58 272 128 300 154 C322 172 345 176 372 176 L372 212 Z" />
      <path className="s-ink" strokeWidth="1.5" d="M24 196 C100 194 140 190 170 160 C195 132 205 66 232 62 C262 58 272 128 300 154 C322 172 345 176 372 176" />
      {/* the weir */}
      <path className="ink" d="M372 150 h14 v62 h-14 z" />
      <line className="s-acc" strokeDasharray="4 4" strokeWidth="1" x1="386" y1="176" x2="736" y2="176" />
      {/* origin side: flat */}
      <path className="f-acc" strokeWidth="1.5" d="M386 176 H736 V212 H386 Z" />
      <text className="ink big" x="24" y="24">Requests whose entry just expired</text>
      <text className="mut" x="24" y="40">10,000 keys, max-age=60, stored within 1.7 s</text>
      <text className="ink big" x="404" y="134">Origin fetches in flight</text>
      <text className="mut" x="404" y="150">peak 32, the configured limit</text>
      <text className="acc" x="596" y="170">MaxConcurrent = 32</text>
      <text className="mut" x="372" y="232" textAnchor="middle">weir</text>
      <text className="mut" x="24" y="232">time</text>
      <text className="mut" x="736" y="232" textAnchor="end">all 10,000 refetched, 0 client errors</text>
    </Svg>
  )
}

const stages: [string, string, string?][] = [
  ['1', 'closed?', 'ErrClosed'],
  ['2', 'validate and classify', '400'],
  ['3', 'unsafe or unknown method', 'pass through'],
  ['4', 'bypass rule', 'pass through'],
  ['5', 'build forwarded request and key'],
  ['6', 'store lookup: primary, then variant'],
  ['7', 'epoch check (purges)'],
  ['8', 'decide: serve, serve stale, coalesced fetch'],
  ['9', 'record: event, miss rate, Cache-Status'],
]

export function RequestFlow() {
  const row = 40
  return (
    <Svg id="flow" w={560} h={stages.length * row + 30} title="The nine stages of Serve" desc="Nine stages run in order. Stages one to four can end the request early. Stages two and five are the security boundary.">
      {stages.map(([n, label, exit], i) => {
        const y = 14 + i * row
        const boundary = n === '2' || n === '5'
        return (
          <g key={n}>
            {i > 0 && <line className="axis" x1="34" y1={y - 12} x2="34" y2={y} />}
            <rect className={boundary ? 'f-acc' : 'f-box'} x="16" y={y} width="330" height="28" rx="4" />
            <text className="mut" x="28" y={y + 18}>{n}</text>
            <text className="ink" x="52" y={y + 18}>{label}</text>
            {exit && (
              <>
                <line className="s-mut" strokeDasharray="3 3" x1="346" y1={y + 14} x2="400" y2={y + 14} />
                <text className="mut" x="408" y={y + 18}>{exit}</text>
              </>
            )}
            {n === '5' && <text className="acc" x="356" y={y + 18}>security boundary ends here</text>}
          </g>
        )
      })}
    </Svg>
  )
}

/** One entry's life with max-age=60, stale-while-revalidate=30, stale-if-error=300 and default settings. */
export function FreshnessTimeline() {
  // Segment edges in px. The axis is not to scale: the last two windows run for minutes.
  const e = [12, 196, 244, 346, 452, 548]
  const ticks = ['0 s', '54 s', '60 s', '90 s', '360 s', '660 s']
  return (
    <Svg id="life" w={560} h={200} title="Lifetime of one entry" desc="Stored at zero seconds. Jitter moves expiry from 60 seconds to somewhere between 54 and 60. Early refresh may fire shortly before expiry. Stale-while-revalidate covers the next 30 seconds, then stale-if-error applies on errors only until 360 seconds, then the entry is kept 5 more minutes for revalidation.">
      <text className="acc" x={(e[1] + e[2]) / 2} y="16" textAnchor="middle">jitter: expiry lands in here</text>
      <path className="s-acc" strokeWidth="1.5" d={`M${e[1]} 26 V34 M${e[2]} 26 V34 M${e[1]} 30 H${e[2]}`} />
      <rect className="f-acc" x={e[0]} y="44" width={e[1] - e[0]} height="34" />
      <rect className="f-acc" x={e[1]} y="44" width={e[2] - e[1]} height="34" opacity="0.45" />
      <rect className="f-wrn" x={e[2]} y="44" width={e[3] - e[2]} height="34" />
      <rect className="f-box" x={e[3]} y="44" width={e[4] - e[3]} height="34" />
      <rect className="f-box" x={e[4]} y="44" width={e[5] - e[4]} height="34" strokeDasharray="4 3" />
      <text className="ink big" x={e[0] + 10} y="66">fresh</text>
      <text className="ink" x={e[2] + 8} y="66">served stale</text>
      <text className="ink" x={e[3] + 8} y="66">on error only</text>
      <text className="mut" x={e[4] + 8} y="66">304 only</text>
      {e.map((x, i) => (
        <text key={x} className="mut" x={x} y="98" textAnchor={i === 0 ? 'start' : i === e.length - 1 ? 'end' : 'middle'}>{ticks[i]}</text>
      ))}
      <path className="s-ink" d="M150 78 V128" />
      <text className="ink" x="150" y="144" textAnchor="middle">early refresh may start</text>
      <text className="mut" x="150" y="160" textAnchor="middle">more likely as expiry nears</text>
      <text className="mut" x="548" y="144" textAnchor="end">axis not to scale</text>
      <text className="mut" x="12" y="190">max-age=60, stale-while-revalidate=30, stale-if-error=300</text>
    </Svg>
  )
}

/** Default limiter sizes: 64 slots, 16 reserved for foreground, 16 per partition, upload pool 16, queue 1024. */
export function Budget() {
  const step = 8.25
  const slots = (n: number, y: number, cls: (i: number) => string) =>
    Array.from({ length: n }, (_, i) => <rect key={i} className={cls(i)} x={16 + i * step} y={y} width={step - 1.6} height="22" rx="1.5" />)
  return (
    <Svg id="budget" w={560} h={250} title="The origin concurrency budget at defaults" desc="64 main slots, of which background refreshes may use at most 48 and any one path at most 16. A separate pool of 16 slots serves requests with a body. At most 80 fetches reach the origin at once. A queue of 8,192 waits up to 5 seconds; beyond that requests are shed.">
      <text className="ink big" x="16" y="20">MaxConcurrent = 64 slots</text>
      {slots(64, 30, (i) => (i < 48 ? 'f-acc' : 'f-box'))}
      <path className="s-mut" d={`M16 60 H${16 + 48 * step - 3}`} />
      <text className="mut" x="16" y="76">background refreshes: at most 48</text>
      <path className="s-mut" d={`M${16 + 48 * step} 60 H${16 + 64 * step - 2}`} />
      <text className="mut" x={16 + 64 * step - 2} y="76" textAnchor="end">16 kept for foreground</text>
      <path className="s-ink" strokeWidth="1.5" d={`M16 92 H${16 + 16 * step - 2}`} />
      <text className="ink" x="16" y="108">MaxPerPartition = 16: one path cannot take more</text>
      <text className="ink big" x="16" y="142">MaxUpload = 16 slots</text>
      {slots(16, 152, () => 'f-box')}
      <text className="mut" x={16 + 16 * step + 10} y="167">separate pool for requests with a body</text>
      <text className="mut" x="16" y="192">The origin sees at most 64 + 16 = 80 fetches at once.</text>
      <rect className="f-wrn" x="16" y="208" width="528" height="26" rx="3" />
      <text className="wrn" x="26" y="225">queue: 8,192 waiters, 5 s each, then stale or 503 with Retry-After</text>
    </Svg>
  )
}

/** Bars for the hit-path table on the benchmarks page; same data file as the check script reads. */
export function HitPathChart() {
  const rows = bench.hitPath.rows.map((r) => ({ ...r, v: Number(r.ns.replace(/\s/g, '')) }))
  const max = 1200
  const x0 = 170
  const w = 330
  return (
    <Svg id="hit" w={560} h={rows.length * 38 + 44} title={bench.hitPath.title} desc={rows.map((r) => `${r.build}: ${r.ns} nanoseconds per operation, ${r.allocs} allocations`).join('. ')}>
      {rows.map((r, i) => {
        const y = 10 + i * 38
        return (
          <g key={r.build}>
            <text className="ink" x="0" y={y + 17}>{r.build}</text>
            <rect className={i === rows.length - 1 ? 'f-acc' : 'f-box'} x={x0} y={y} width={(r.v / max) * w} height="24" rx="2" />
            <text className="ink" x={x0 + (r.v / max) * w + 8} y={y + 17}>{r.ns} ns</text>
          </g>
        )
      })}
      <line className="axis" x1={x0} y1="4" x2={x0} y2={rows.length * 38 + 6} />
      <text className="mut" x={x0} y={rows.length * 38 + 28}>0</text>
      <text className="mut" x={x0 + w} y={rows.length * 38 + 28} textAnchor="end">1 200 ns/op</text>
      <text className="mut" x="0" y={rows.length * 38 + 28}>lower is better</text>
    </Svg>
  )
}

export const figures: Record<string, () => React.ReactNode> = {
  'request-flow': RequestFlow,
  'freshness-timeline': FreshnessTimeline,
  budget: Budget,
  hydrograph: () => Hydrograph({}),
  'hit-path': HitPathChart,
}
