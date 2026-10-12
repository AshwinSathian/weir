import fs from 'node:fs'
import path from 'node:path'

// The repository is the source of truth. Pages read it at build time, so a
// fact has one home (plan §4). Every helper throws on a missing anchor: a
// renamed heading must fail the build, not render an empty page.
const ROOT = path.resolve(process.cwd(), '..')

export function repoFile(rel: string): string {
  return fs.readFileSync(path.join(ROOT, rel), 'utf8')
}

/** The body of the section whose heading line starts with `heading` (for example "## 6. Configuration"). */
export function section(rel: string, heading: string, opts: { keepHeading?: boolean } = {}): string {
  const lines = repoFile(rel).split('\n')
  const start = lines.findIndex((l) => l.startsWith(heading))
  if (start < 0) throw new Error(`site: ${rel} has no heading starting with "${heading}"`)
  const level = /^#+/.exec(heading)?.[0].length ?? 2
  let end = lines.length
  for (let i = start + 1; i < lines.length; i++) {
    const m = /^(#+) /.exec(lines[i])
    if (m && m[1].length <= level) {
      end = i
      break
    }
  }
  const body = lines.slice(opts.keepHeading ? start : start + 1, end).join('\n').trim()
  if (!body) throw new Error(`site: section "${heading}" in ${rel} is empty`)
  return body
}

/** Rows of every Markdown table in `md` whose header starts with `firstHeader`. */
export function tableRows(md: string, firstHeader: string): string[][] {
  const rows: string[][] = []
  const lines = md.split('\n')
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].startsWith(`| ${firstHeader} |`)) continue
    for (let j = i + 2; j < lines.length && lines[j].startsWith('|'); j++) {
      rows.push(lines[j].slice(1, -1).split(/(?<!\\)\|/).map((c) => c.trim()))
    }
  }
  if (!rows.length) throw new Error(`site: no table with first header "${firstHeader}"`)
  return rows
}

/** Shift every heading in `md` by `n` levels (negative promotes, never above h2), so a repo section fits under a page title. */
export function demote(md: string, n: number): string {
  return md.replace(/^(#+) /gm, (_, h: string) => '#'.repeat(Math.max(2, Math.min(6, h.length + n))) + ' ')
}

/** The body of a tested Example function, without the signature and closing brace. */
export function goExample(rel: string, name: string): string {
  const src = repoFile(rel)
  const m = new RegExp(`^func ${name}\\(\\) \\{\\n([\\s\\S]*?)\\n\\}\\n`, 'm').exec(src)
  if (!m) throw new Error(`site: ${rel} has no func ${name}`)
  return m[1].replace(/^\t/gm, '')
}

/** The first fenced block of `lang` in a repo Markdown file. */
export function fenced(rel: string, lang: string): string {
  const m = new RegExp('```' + lang + '\\n([\\s\\S]*?)\\n```').exec(repoFile(rel))
  if (!m) throw new Error(`site: ${rel} has no ${lang} block`)
  return m[1]
}

/** The body of func main in the README program, unindented. */
export function readmeMain(): string {
  const m = /func main\(\) \{\n([\s\S]*?)\n\}/.exec(fenced('README.md', 'go'))
  if (!m) throw new Error('site: README program has no func main')
  return m[1].replace(/^\t/gm, '')
}
