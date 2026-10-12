import Link from 'next/link'
import { docsNav, type NavItem } from '@/lib/nav'
import type { Heading } from '@/lib/md'
import { REPO } from '@/data/release'
import { figures } from './diagrams'

/** Rendered Markdown, with {{figure name}} markers replaced by diagrams. */
export function Prose({ html }: { html: string }) {
  const parts = html.split(/<div data-figure="([\w-]+)"><\/div>/)
  return (
    <div className="prose">
      {parts.map((part, i) => {
        if (i % 2 === 0) return <div key={i} dangerouslySetInnerHTML={{ __html: part }} />
        const Fig = figures[part]
        if (!Fig) throw new Error(`site: unknown figure "${part}"`)
        return <figure key={i} className="hydro"><Fig /></figure>
      })}
    </div>
  )
}

export function Crumbs({ trail }: { trail: NavItem[] }) {
  return (
    <nav className="breadcrumbs" aria-label="Breadcrumb">
      <Link href="/">Weir</Link>
      {trail.map((t, i) => (
        <span key={t.href}>
          / {i === trail.length - 1 ? <span aria-current="page">{t.title}</span> : <Link href={t.href}>{t.title}</Link>}
        </span>
      ))}
    </nav>
  )
}

export function PageFoot({ source }: { source: string }) {
  return (
    <div className="page-foot">
      <a href={`${REPO}/edit/main/${source}`}>Edit this page on GitHub</a>
      <a href={`${REPO}/issues/new`}>Report a mistake</a>
    </div>
  )
}

function Toc({ headings }: { headings: Heading[] }) {
  const h = headings.filter((x) => x.id)
  if (h.length < 2) return <div className="toc" />
  return (
    <nav className="toc" aria-label="On this page">
      <div className="label">On this page</div>
      <ul>
        {h.map((x) => (
          <li key={x.id} className={x.depth === 3 ? 'd3' : ''}><a href={`#${x.id}`}>{x.text}</a></li>
        ))}
      </ul>
    </nav>
  )
}

const flat = docsNav.flatMap((g) => g.items)

/** Three zones: navigation, prose, on-this-page. */
export function DocShell(p: { path: string; title: string; description: string; html: string; headings: Heading[]; source: string }) {
  const i = flat.findIndex((n) => n.href === p.path)
  const prev = i > 0 ? flat[i - 1] : undefined
  const next = i >= 0 && i < flat.length - 1 ? flat[i + 1] : undefined
  const group = docsNav.find((g) => g.items.some((n) => n.href === p.path))
  return (
    <div className="wrap docs">
      <nav className="sidenav" aria-label="Documentation">
        {docsNav.map((g) => (
          <div key={g.title}>
            <div className="label">{g.title}</div>
            <ul>
              {g.items.map((n) => (
                <li key={n.href}><Link href={n.href} aria-current={n.href === p.path ? 'page' : undefined}>{n.title}</Link></li>
              ))}
            </ul>
          </div>
        ))}
      </nav>
      <article data-pagefind-body>
        <Crumbs trail={[{ title: 'Docs', href: '/docs/quickstart/' }, ...(group ? [{ title: group.title, href: group.items[0].href }] : []), { title: p.title, href: p.path }]} />
        <header className="page-head">
          <h1>{p.title}</h1>
          <p className="lede">{p.description}</p>
        </header>
        <Prose html={p.html} />
        <nav className="pager" aria-label="Previous and next page">
          {prev && <Link href={prev.href}><span className="label">Previous</span>{prev.title}</Link>}
          {next && <Link className="next" href={next.href}><span className="label">Next</span>{next.title}</Link>}
        </nav>
        <PageFoot source={p.source} />
      </article>
      <Toc headings={p.headings} />
    </div>
  )
}

/** One column for pages outside the docs tree. */
export function PageShell(p: { path: string; title: string; description: string; source: string; wide?: boolean; children: React.ReactNode }) {
  return (
    <div className="wrap">
      <article className={p.wide ? 'page wide' : 'page'} data-pagefind-body>
        <Crumbs trail={[{ title: p.title, href: p.path }]} />
        <header className="page-head">
          <h1>{p.title}</h1>
          <p className="lede">{p.description}</p>
        </header>
        {p.children}
        <PageFoot source={p.source} />
      </article>
    </div>
  )
}
