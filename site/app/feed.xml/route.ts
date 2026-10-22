import { text } from '@/lib/pages'
import { repoFile } from '@/lib/repo'
import { REPO, SITE } from '@/data/release'

export const dynamic = 'force-static'

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

// Repository-relative Markdown links, absolute so a feed reader can follow them.
const absolute = (md: string) => md.replace(/\]\((?!https?:|#|\/)([^)]+)\)/g, `](${REPO}/blob/main/$1)`)

// One Atom entry per CHANGELOG section. An undated section ("unreleased") is skipped.
export function GET() {
  const parts = repoFile('CHANGELOG.md').split(/^## /m).slice(1)
  const entries = parts.flatMap((p) => {
    const m = /^(v[\w.-]+) \((\d{4}-\d{2}-\d{2})\)\n([\s\S]*)$/.exec(p)
    if (!m) return []
    const [, version, date, body] = m
    return [`  <entry>
    <id>${REPO}/releases/tag/${version}</id>
    <title>Weir ${version}</title>
    <updated>${date}T00:00:00Z</updated>
    <link href="${SITE}/changelog/#${version.replace(/\./g, '')}-${date}"/>
    <content type="text">${esc(absolute(body.trim()))}</content>
  </entry>`]
  })
  const updated = /<updated>(.*?)<\/updated>/.exec(entries[0] ?? '')?.[1] ?? '2026-10-12T00:00:00Z'
  return text(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>${SITE}/feed.xml</id>
  <title>Weir releases</title>
  <updated>${updated}</updated>
  <author><name>Ashwin Sathian</name></author>
  <link rel="self" href="${SITE}/feed.xml"/>
  <link href="${SITE}/changelog/"/>
${entries.join('\n')}
</feed>
`, 'application/atom+xml')
}
