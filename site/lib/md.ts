import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkGfm from 'remark-gfm'
import remarkRehype from 'remark-rehype'
import rehypeRaw from 'rehype-raw'
import rehypeSlug from 'rehype-slug'
import rehypeShiki from '@shikijs/rehype'
import rehypeStringify from 'rehype-stringify'
import { REPO } from '@/data/release'

export type Heading = { depth: number; id: string; text: string }
export type Rendered = { html: string; headings: Heading[] }

type Node = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: Node[] }

const blob = (path: string) => `${REPO}/blob/main/${path}`

// Requirement, threat, decision and card IDs, and section references to the
// design documents, are how the repository cross-references itself; a reader
// of the site gets nothing from them. A parenthesis that holds only such
// references is dropped from prose, and an ID left in running text is plain
// text. The threat table keeps its row labels (idRows).
const IDTOK = String.raw`(?:FR-[A-Z]+-\d+|NFR-\d+|INV-\d+|T6\.\d+[a-z]?|T-\d+|R-\d{1,2}|D-H\d+|D\d{1,2}|E-\d+|S-\d+|V-\d+|P[1-8]|OQ-[A-Z0-9]+(?:-\d+)?|HRD-\d+[a-z]?|CMP-[A-Z0-9-]+|M\d+-\d+[a-z]?|P2-\d+[a-z]?|P25-\d+[a-z]?|(?:docs\/)?\d\d §[\d.]+[a-z]?(?: step \d+)?|§\d+(?:\.\d+)*[a-z]?)`
const REF = String.raw`(?:\d\d )?${IDTOK}` // "06 R-2": a risk in document 06
const LIST = String.raw`(?:(?:decisions?|cards?|see|threat|requirement|per) )?${REF}(?:(?:, |; | and | to )${REF})*`
const PAREN = new RegExp(String.raw`\s*\(${LIST}\)`, 'g')
const WHOLE = new RegExp(String.raw`^\s*${LIST}\s*$`) // a table cell that is only references
const LEAD = new RegExp(String.raw`^\s*${LIST}[;:]\s*`) // "FR-LIM-3; MaxConcurrent or more: ..."
const TAIL = new RegExp(String.raw`(?:, |; )${LIST}(?=\))`, 'g') // "(XForwardedSet, FR-FWD-8)"
const HEAD = new RegExp(String.raw`\(${LIST}(?:, |; )`, 'g') // "(FR-FWD-10, v1.1; ...)"

/** stripRefs removes reference-only parentheses, and a cell's leading references. */
export function stripRefs(v: string): string {
  if (WHOLE.test(v)) return ''
  return v.replace(LEAD, '').replace(PAREN, '').replace(TAIL, '').replace(HEAD, '(')
}

// Columns that only cross-reference the repository: decisions, requirement
// IDs and Go test names. A site table drops them.
const REF_COLUMNS = new Set(['Decision', 'Decisions', 'Refs', 'Requirements', 'Test', 'Tests (Go)'])

/** dropRefColumns removes REF_COLUMNS from a table element in place. */
function dropRefColumns(table: Node) {
  const rows: Node[] = []
  const collect = (n: Node) => {
    for (const c of n.children ?? []) {
      if (c.tagName === 'tr') rows.push(c)
      else if (c.tagName === 'thead' || c.tagName === 'tbody') collect(c)
    }
  }
  collect(table)
  const head = rows[0]?.children?.filter((c) => c.tagName === 'th' || c.tagName === 'td') ?? []
  const drop = new Set(head.flatMap((c, i) => (REF_COLUMNS.has(text(c).trim()) ? [i] : [])))
  if (drop.size === 0 || drop.size === head.length) return
  for (const r of rows) {
    let i = 0
    r.children = r.children?.filter((c) => (c.tagName === 'th' || c.tagName === 'td' ? !drop.has(i++) : true))
  }
}

const text = (n: Node): string => (n.type === 'text' ? (n.value ?? '') : (n.children ?? []).map(text).join(''))

function resolve(base: string, href: string): string {
  const parts = (base + href).split('/')
  const out: string[] = []
  for (const p of parts) {
    if (p === '..') out.pop()
    else if (p !== '.') out.push(p)
  }
  return out.join('/')
}

/** rehype plugin: ID badges, repo-relative links, table wrappers, callouts, heading list. */
function weirRehype(opts: { base: string; headings: Heading[]; idRows: boolean }) {
  const walk = (node: Node, inside: string[]) => {
    if (!node.children) return
    const next: Node[] = []
    for (const child of node.children) {
      if (child.type === 'text' && !inside.some((t) => ['pre', 'code'].includes(t))) {
        child.value = stripRefs(child.value ?? '')
        next.push(child)
        continue
      }
      if (child.type === 'element') {
        const tag = child.tagName ?? ''
        const props = (child.properties ??= {})
        if (tag === 'a' && typeof props.href === 'string') {
          const h = props.href
          if (!/^(https?:|mailto:|\/|#)/.test(h)) props.href = blob(resolve(opts.base, h))
          if (/^https?:/.test(props.href as string)) props.rel = 'noopener'
        }
        if (tag === 'h2' || tag === 'h3') {
          opts.headings.push({ depth: tag === 'h2' ? 2 : 3, id: String(props.id ?? ''), text: text(child) })
        }
        if (tag === 'blockquote') {
          const p = child.children?.find((c) => c.type === 'element')
          const first = p?.children?.[0]
          const m = first?.type === 'text' ? /^\[!(NOTE|CAUTION|PROTECTION|LIMITS)\]\s*/.exec(first.value ?? '') : null
          if (m && first) {
            first.value = (first.value ?? '').slice(m[0].length)
            props.className = ['callout', m[1].toLowerCase()]
            props.role = 'note'
          }
        }
        // Rows whose first cell is a threat or risk ID become link targets.
        if (opts.idRows && tag === 'tr') {
          const cell = child.children?.find((c) => c.tagName === 'td')
          const id = cell ? text(cell).trim() : ''
          if (/^(T|R)-\d+$/.test(id)) props.id = id.toLowerCase()
        }
        walk(child, [...inside, tag])
        if (tag === 'table') {
          dropRefColumns(child)
          next.push({ type: 'element', tagName: 'div', properties: { className: ['table-wrap'], tabIndex: 0 }, children: [child] })
          continue
        }
      }
      next.push(child)
    }
    node.children = next
  }
  return (tree: Node) => walk(tree, [])
}

/** Render Markdown to HTML. `base` is the repo directory the source lives in, for relative links. */
export async function render(markdown: string, opts: { base?: string; idRows?: boolean } = {}): Promise<Rendered> {
  const headings: Heading[] = []
  const file = await unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkRehype, { allowDangerousHtml: true })
    .use(rehypeRaw)
    .use(rehypeSlug)
    .use(weirRehype, { base: opts.base ?? '', headings, idRows: opts.idRows ?? false })
    .use(rehypeShiki, {
      themes: { light: 'github-light', dark: 'github-dark-dimmed' },
      defaultColor: false,
      defaultLanguage: 'text',
      fallbackLanguage: 'text',
    })
    .use(rehypeStringify)
    .process(markdown)
  return { html: String(file), headings }
}
