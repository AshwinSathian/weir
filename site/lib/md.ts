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

// Where a requirement, threat or risk ID points. Threats and residual risks
// have pages here; everything else lives in a normative document on GitHub.
function idHref(id: string): string {
  if (/^T-\d+$/.test(id)) return `/security/threats/#${id.toLowerCase()}`
  if (/^R-\d+$/.test(id)) return `/security/#${id.toLowerCase()}`
  if (/^INV-\d+$/.test(id)) return blob('docs/06-threat-model.md#4-security-invariants')
  if (/^D\d+$/.test(id)) return blob('docs/01-technical-spec.md#2-decisions-locked-before-this-spec')
  return blob('docs/01-technical-spec.md')
}

const ID = /\b(FR-[A-Z]+-\d+|NFR-\d+|INV-\d+|T-\d+|R-\d{1,2}|D\d{1,2})\b/g

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
      if (child.type === 'text' && !inside.some((t) => ['a', 'pre', 'code', 'h1', 'h2', 'h3', 'h4'].includes(t))) {
        const v = child.value ?? ''
        let last = 0
        for (const m of v.matchAll(ID)) {
          if (m.index > last) next.push({ type: 'text', value: v.slice(last, m.index) })
          next.push({
            type: 'element',
            tagName: 'a',
            properties: { className: ['id'], href: idHref(m[0]) },
            children: [{ type: 'text', value: m[0] }],
          })
          last = m.index + m[0].length
        }
        if (last === 0) next.push(child)
        else if (last < v.length) next.push({ type: 'text', value: v.slice(last) })
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
