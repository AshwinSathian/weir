import { text } from '@/lib/pages'
import { SITE } from '@/data/release'

export const dynamic = 'force-static'

export function GET() {
  return text(`User-agent: *\nAllow: /\n\nSitemap: ${SITE}/sitemap.xml\n`, 'text/plain')
}
