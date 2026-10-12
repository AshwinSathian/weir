import { allPages, text } from '@/lib/pages'

export const dynamic = 'force-static'

export function GET() {
  const urls = allPages().map((p) => `  <url><loc>${p.url}</loc></url>`).join('\n')
  return text(`<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`, 'application/xml')
}
