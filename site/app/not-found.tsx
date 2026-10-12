import Link from 'next/link'

export const metadata = { title: 'Page not found' }

export default function NotFound() {
  return (
    <div className="wrap">
      <div className="page">
        <header className="page-head">
          <h1>Nothing is stored for this URL</h1>
          <p className="lede"><code>Cache-Status: Weir; fwd=uri-miss; fwd-status=404</code></p>
        </header>
        <div className="prose">
          <p>The page may have moved. Try the search in the header, or start from one of these:</p>
          <ul>
            <li><Link href="/docs/quickstart/">Quickstart</Link></li>
            <li><Link href="/docs/concepts/">Concepts</Link></li>
            <li><Link href="/docs/reference/config/">Configuration reference</Link></li>
            <li><Link href="/">Home</Link></li>
          </ul>
        </div>
      </div>
    </div>
  )
}
