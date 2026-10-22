import Link from 'next/link'
import { docsNav, topNav } from '@/lib/nav'
import { RC_VERSION, RELEASED, REPO, SHOWN_VERSION } from '@/data/release'
import { MenuButton, Search, ThemeToggle } from './client'

/** A weir in profile: the pool on the left, the crest, the lower water on the right. */
export function Mark({ size = 22 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path d="M1 8h9.5v9H23" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinejoin="round" />
      <path d="M1 12.5h6M14 21h9" fill="none" stroke="currentColor" strokeWidth="1.4" opacity="0.55" />
    </svg>
  )
}

export function Header() {
  return (
    <>
      <a className="skip" href="#main">Skip to content</a>
      {!RELEASED && (
        <div className="banner">
          <div className="wrap">
            Pre-release. Install lines use <code>{RC_VERSION}</code> until <code>v1.0.0</code> is tagged for all four modules.{' '}
            <Link href="/changelog/">Changelog</Link>
          </div>
        </div>
      )}
      <header className="site-header">
        <div className="wrap">
          <Link className="logo" href="/" aria-label="Weir home">
            <Mark />
            <span>Weir</span>
          </Link>
          <nav className="site-nav" aria-label="Main">
            {topNav.map((n) => (
              <Link key={n.href} href={n.href}>{n.title}</Link>
            ))}
          </nav>
          <div className="header-tools">
            <Search />
            <ThemeToggle />
            <a className="icon-btn" href={REPO} aria-label="Weir on GitHub">
              <svg width="18" height="18" viewBox="0 0 16 16" aria-hidden="true">
                <path fill="currentColor" d="M8 0a8 8 0 0 0-2.53 15.59c.4.07.55-.17.55-.38v-1.33c-2.23.48-2.7-1.07-2.7-1.07-.36-.93-.89-1.17-.89-1.17-.73-.5.05-.49.05-.49.8.06 1.23.83 1.23.83.71 1.22 1.87.87 2.33.66.07-.52.28-.87.5-1.07-1.77-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 4 0c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.28.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48v2.19c0 .21.15.46.55.38A8 8 0 0 0 8 0z" />
              </svg>
            </a>
            <MenuButton />
          </div>
        </div>
        <nav id="mobile-nav" className="mobile-nav" aria-label="All pages" hidden>
          {topNav.slice(2).map((n) => (
            <Link key={n.href} href={n.href}>{n.title}</Link>
          ))}
          {docsNav.map((g) => (
            <div key={g.title}>
              <div className="label">{g.title}</div>
              {g.items.map((n) => (
                <Link key={n.href} href={n.href}>{n.title}</Link>
              ))}
            </div>
          ))}
        </nav>
      </header>
    </>
  )
}

export function Footer() {
  return (
    <footer className="site-footer">
      <div className="wrap">
        <div>
          Weir {SHOWN_VERSION}, by <Link href="/about/">Ashwin Sathian</Link>. Code and documentation under{' '}
          <a href={`${REPO}/blob/main/LICENSE`}>Apache-2.0</a>. No cookies, no analytics.
        </div>
        <nav aria-label="Footer">
          <Link href="/security/">Security policy</Link>
          <Link href="/changelog/">Changelog</Link>
          <Link href="/about/">About</Link>
          <a href="/feed.xml">Release feed</a>
          <a href={REPO}>GitHub</a>
        </nav>
      </div>
    </footer>
  )
}
