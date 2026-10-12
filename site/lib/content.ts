import fs from 'node:fs'
import path from 'node:path'
import matter from 'gray-matter'
import type { Metadata } from 'next'
import { render, type Rendered } from './md'
import { generatedTables } from './tables'
import { demote, fenced, goExample, repoFile, section } from './repo'
import { CADDY_VERSION, GO_DEPLOY, GO_MIN, RC_VERSION, RELEASED, SHOWN_VERSION, SITE } from '@/data/release'

const CONTENT = path.join(process.cwd(), 'content')

export type Doc = Rendered & { title: string; description: string; source: string; slug: string[] }

const vars: Record<string, string> = { version: SHOWN_VERSION, go_min: GO_MIN, go_deploy: GO_DEPLOY, caddy_version: CADDY_VERSION }

/**
 * Directives, each alone on a line or inline for {{var}}:
 *   {{include docs/runbook.md "## 4. Incidents" demote=1}}   a section of a repo document
 *   {{example example_test.go ExampleEngine_Serve}}          a tested Example body as a Go block
 *   {{file CHANGELOG.md}}                                    a whole repo file
 *   {{figure name}}                                          a React diagram, placed by the page
 */
function expand(md: string): string {
  // Install lines in repo documents name the release; before the tags exist the site shows the rc.
  const pin = (s: string) => (RELEASED ? s : s.replaceAll('@v1.0.0', '@' + RC_VERSION))
  return md
    .replace(/^\{\{include (\S+) "(.+?)"(?: demote=(-?\d))?( heading)?\}\}$/gm, (_, f, h, d, keep) =>
      pin(demote(section(f, h, { keepHeading: Boolean(keep) }), Number(d ?? 0))),
    )
    .replace(/^\{\{example (\S+) (\w+)\}\}$/gm, (_, f, n) => '```go\n' + goExample(f, n) + '\n```')
    .replace(/^\{\{fenced (\S+) (\w+)\}\}$/gm, (_, f, l) => '```' + l + '\n' + fenced(f, l) + '\n```')
    .replace(/^\{\{file (\S+)\}\}$/gm, (_, f) => pin(repoFile(f)))
    .replace(/^\{\{figure ([\w-]+)\}\}$/gm, (_, n) => `<div data-figure="${n}"></div>`)
    .replace(/^\{\{changelog\}\}$/gm, () => {
      const md = repoFile('CHANGELOG.md')
      return pin(md.slice(md.indexOf('\n## ')))
    })
    .replace(/\{\{(\w+)\}\}/g, (m, k) => vars[k] ?? generatedTables()[k] ?? m)
}

export async function loadContent(rel: string, opts: { base?: string; idRows?: boolean } = {}): Promise<Doc> {
  const source = `site/content/${rel}.md`
  const raw = fs.readFileSync(path.join(CONTENT, rel + '.md'), 'utf8')
  const { data, content } = matter(raw)
  if (!data.title || !data.description) throw new Error(`site: ${source} needs title and description`)
  const out = await render(expand(content), { base: opts.base ?? data.base ?? '', idRows: opts.idRows ?? Boolean(data.idRows) })
  return { ...out, title: data.title, description: data.description, source, slug: rel.split('/') }
}

export function docSlugs(): string[][] {
  const out: string[][] = []
  const walk = (dir: string, prefix: string[]) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.isDirectory()) walk(path.join(dir, e.name), [...prefix, e.name])
      else if (e.name.endsWith('.md')) out.push(e.name === 'index.md' ? prefix : [...prefix, e.name.slice(0, -3)])
    }
  }
  walk(path.join(CONTENT, 'docs'), [])
  return out.filter((s) => s.length > 0)
}

export function docFile(slug: string[]): string {
  const direct = 'docs/' + slug.join('/')
  return fs.existsSync(path.join(CONTENT, direct + '.md')) ? direct : direct + '/index'
}

export function docMeta(slug: string[]): { title: string; description: string } {
  const { data } = matter(fs.readFileSync(path.join(CONTENT, docFile(slug) + '.md'), 'utf8'))
  return { title: data.title, description: data.description }
}

export function meta(title: string, description: string, pathname: string): Metadata {
  const url = SITE + pathname
  return {
    title,
    description,
    alternates: { canonical: url },
    openGraph: { title: `${title} · Weir`, description, url, siteName: 'Weir', type: 'article', images: ['/og.png'] },
    twitter: { card: 'summary_large_image' },
  }
}
