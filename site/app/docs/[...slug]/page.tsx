import { DocShell } from '@/components/shell'
import { docFile, docMeta, docSlugs, loadContent, meta } from '@/lib/content'

export const dynamicParams = false

export function generateStaticParams() {
  return docSlugs().map((slug) => ({ slug }))
}

type Props = { params: Promise<{ slug: string[] }> }

export async function generateMetadata({ params }: Props) {
  const { slug } = await params
  const m = docMeta(slug)
  return meta(m.title, m.description, `/docs/${slug.join('/')}/`)
}

export default async function Page({ params }: Props) {
  const { slug } = await params
  const doc = await loadContent(docFile(slug))
  return <DocShell path={`/docs/${slug.join('/')}/`} {...doc} />
}
