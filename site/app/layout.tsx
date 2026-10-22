import type { Metadata, Viewport } from 'next'
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import '@/styles/site.css'
import { Footer, Header } from '@/components/chrome'
import { Enhance } from '@/components/client'
import { SITE } from '@/data/release'

export const metadata: Metadata = {
  metadataBase: new URL(SITE),
  title: { default: 'Weir: shared HTTP caching for Go', template: '%s · Weir' },
  description: 'A Go library for shared HTTP caching in front of an origin that has to stay up.',
  icons: { icon: [{ url: '/favicon.svg', type: 'image/svg+xml' }, { url: '/favicon.ico', sizes: '32x32' }], apple: '/apple-touch-icon.png' },
  // Pages with their own metadata (lib/content.ts meta) replace these; the
  // home page and any page without them share the site's card.
  openGraph: {
    title: 'Weir: shared HTTP caching for Go',
    description: 'A Go library for shared HTTP caching in front of an origin that has to stay up.',
    siteName: 'Weir',
    type: 'website',
    images: [{ url: '/og.png', width: 1200, height: 630, alt: 'Weir: shared HTTP caching for Go' }],
  },
  twitter: { card: 'summary_large_image' },
  alternates: { types: { 'application/atom+xml': '/feed.xml' } },
}

export const viewport: Viewport = { themeColor: '#0d1418' }

// Runs before first paint so a stored choice never flashes the other theme.
// postbuild.mjs puts this script's hash in the page's Content-Security-Policy.
const theme = `try{var t=localStorage.getItem('theme');if(t==='light')document.documentElement.dataset.theme=t}catch(e){}`

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: theme }} />
      </head>
      <body>
        <Header />
        <main id="main">{children}</main>
        <Footer />
        <Enhance />
      </body>
    </html>
  )
}
