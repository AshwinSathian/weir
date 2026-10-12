import { docMeta, docSlugs } from './content'
import { topPages } from './nav'
import { SITE } from '@/data/release'

/** Every published page, for the sitemap and llms.txt. */
export function allPages(): { url: string; title: string; description: string }[] {
  const docs = docSlugs()
    .map((s) => ({ url: `${SITE}/docs/${s.join('/')}/`, ...docMeta(s) }))
    .sort((a, b) => a.url.localeCompare(b.url))
  return [...topPages.map((p) => ({ url: SITE + p.href, title: p.title, description: p.description })), ...docs]
}

export const text = (body: string, type: string) => new Response(body, { headers: { 'Content-Type': type } })
