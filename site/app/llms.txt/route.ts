import { allPages, text } from '@/lib/pages'
import { MODULE, REPO } from '@/data/release'

export const dynamic = 'force-static'

// https://llmstxt.org: a plain index of the site for language models.
export function GET() {
  const pages = allPages()
  const list = (f: (u: string) => boolean) => pages.filter((p) => f(p.url)).map((p) => `- [${p.title}](${p.url}): ${p.description}`).join('\n')
  return text(
    `# Weir

> Weir is a Go library for shared HTTP caching in front of an origin that has to stay up. It implements RFC 9111 and adds request coalescing, TTL jitter, an origin concurrency limiter, stale serving, a circuit breaker, negative caching, lazy purges and a cache key treated as a security boundary. Adapters exist for net/http and Caddy.

Module: ${MODULE} (Apache-2.0). Source and normative design documents: ${REPO}

## Docs

${list((u) => u.includes('/docs/'))}

## Project

${list((u) => !u.includes('/docs/'))}
`,
    'text/plain; charset=utf-8',
  )
}
