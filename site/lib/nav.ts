export type NavItem = { title: string; href: string }
export type NavGroup = { title: string; items: NavItem[] }

// Grouped by what the reader came to do, not by document type.
export const docsNav: NavGroup[] = [
  {
    title: 'Use Weir',
    items: [
      { title: 'Quickstart', href: '/docs/quickstart/' },
      { title: 'net/http guide', href: '/docs/guides/net-http/' },
      { title: 'Caddy guide', href: '/docs/guides/caddy/' },
      { title: 'Experiments', href: '/docs/guides/experiments/' },
    ],
  },
  {
    title: 'Understand it',
    items: [
      { title: 'Concepts', href: '/docs/concepts/' },
      { title: 'Request flow', href: '/docs/concepts/request-flow/' },
      { title: 'The cache key', href: '/docs/concepts/cache-key/' },
      { title: 'Coalescing', href: '/docs/concepts/coalescing/' },
      { title: 'Freshness and staleness', href: '/docs/concepts/freshness-and-staleness/' },
      { title: 'Origin protection', href: '/docs/concepts/origin-protection/' },
      { title: 'Purging', href: '/docs/concepts/purging/' },
      { title: 'Stores', href: '/docs/concepts/stores/' },
    ],
  },
  {
    title: 'Operate it',
    items: [
      { title: 'Production checklist', href: '/docs/guides/production/' },
      { title: 'Observability', href: '/docs/guides/observability/' },
      { title: 'Incidents', href: '/docs/guides/incidents/' },
      { title: 'Tuning and trade-offs', href: '/docs/tuning/' },
    ],
  },
  {
    title: 'Reference',
    items: [
      { title: 'Configuration', href: '/docs/reference/config/' },
      { title: 'Cache-Status', href: '/docs/reference/cache-status/' },
      { title: 'Errors', href: '/docs/reference/errors/' },
      { title: 'Go API', href: '/docs/reference/api/' },
    ],
  },
]

export const topNav: NavItem[] = [
  { title: 'Docs', href: '/docs/quickstart/' },
  { title: 'Concepts', href: '/docs/concepts/' },
  { title: 'Security', href: '/security/' },
  { title: 'Compare', href: '/compare/' },
  { title: 'Benchmarks', href: '/benchmarks/' },
  { title: 'Changelog', href: '/changelog/' },
]

// Pages outside /docs, for the sitemap, llms.txt and the link checker.
export const topPages: (NavItem & { description: string })[] = [
  { title: 'Weir', href: '/', description: 'A Go library for shared HTTP caching in front of an origin that has to stay up.' },
  { title: 'Security', href: '/security/', description: 'Threat families, invariants, residual risks and how to report a vulnerability.' },
  { title: 'Threat table', href: '/security/threats/', description: 'Every threat in the model with the answer, requirement and test for each.' },
  { title: 'Conformance', href: '/conformance/', description: 'cache-tests results: 284 of 365 pass, and the reason for each of the 81 that do not.' },
  { title: 'Benchmarks', href: '/benchmarks/', description: 'Hit-path cost, five load scenarios and GC cost, with machine, command and limits.' },
  { title: 'Compare', href: '/compare/', description: 'Where Varnish, nginx, Souin, Pingora, singleflight or a CDN is the better choice.' },
  { title: 'Changelog', href: '/changelog/', description: 'Release notes, versioning and support policy.' },
  { title: 'About', href: '/about/', description: 'Where Weir came from and why it has that name.' },
]
