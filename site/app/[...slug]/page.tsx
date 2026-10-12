import fs from 'node:fs'
import path from 'node:path'
import matter from 'gray-matter'
import { PageShell, Prose } from '@/components/shell'
import { loadContent, meta } from '@/lib/content'

// Pages outside /docs: content/pages/<slug>.md or <slug>/index.md.
const DIR = path.join(process.cwd(), 'content/pages')
export const dynamicParams = false

function slugs(dir = DIR, prefix: string[] = []): string[][] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory()
      ? slugs(path.join(dir, e.name), [...prefix, e.name])
      : e.name === 'index.md' ? [prefix] : [[...prefix, e.name.slice(0, -3)]],
  )
}

const file = (slug: string[]) => {
  const direct = 'pages/' + slug.join('/')
  return fs.existsSync(path.join(process.cwd(), 'content', direct + '.md')) ? direct : direct + '/index'
}

export function generateStaticParams() {
  return slugs().map((slug) => ({ slug }))
}

type Props = { params: Promise<{ slug: string[] }> }

export async function generateMetadata({ params }: Props) {
  const { slug } = await params
  const { data } = matter(fs.readFileSync(path.join(process.cwd(), 'content', file(slug) + '.md'), 'utf8'))
  return meta(data.title, data.description, `/${slug.join('/')}/`)
}

export default async function Page({ params }: Props) {
  const { slug } = await params
  const rel = file(slug)
  const { data } = matter(fs.readFileSync(path.join(process.cwd(), 'content', rel + '.md'), 'utf8'))
  const doc = await loadContent(rel)
  return (
    <PageShell path={`/${slug.join('/')}/`} title={doc.title} description={doc.description} source={data.source ?? doc.source} wide={Boolean(data.wide)}>
      <Prose html={doc.html} />
    </PageShell>
  )
}
