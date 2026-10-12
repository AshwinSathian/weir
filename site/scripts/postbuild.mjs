// Runs after `next build`. Three jobs on the static output in out/:
//   1. Content-Security-Policy per page. A static export has inline bootstrap
//      scripts, so each page gets a <meta> policy listing the SHA-256 of its
//      own inline scripts. No 'unsafe-inline' for scripts.
//   2. _headers for Cloudflare Pages: the headers a <meta> cannot set, and caching.
//   3. Link check: every internal href and #fragment must resolve, and every
//      GitHub link into this repository must name a file that exists.
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'

const OUT = path.resolve('out')
const pages = []
;(function walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) walk(p)
    else if (e.name.endsWith('.html')) pages.push(p)
  }
})(OUT)

// 1. CSP
for (const file of pages) {
  let html = fs.readFileSync(file, 'utf8')
  const hashes = new Set()
  for (const m of html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/g)) {
    if (m[1]) hashes.add(`'sha256-${createHash('sha256').update(m[1]).digest('base64')}'`)
  }
  const csp = [
    "default-src 'self'",
    `script-src 'self' 'wasm-unsafe-eval' ${[...hashes].join(' ')}`, // wasm: Pagefind's search index
    "style-src 'self' 'unsafe-inline'", // Shiki and the search UI set style attributes
    "img-src 'self' data:",
    "font-src 'self'",
    "connect-src 'self'",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'none'",
  ].join('; ')
  if (!html.includes('<head>')) throw new Error(`postbuild: no <head> in ${file}`)
  html = html.replace('<head>', `<head><meta http-equiv="Content-Security-Policy" content="${csp}"/>`)
  fs.writeFileSync(file, html)
}

// 2. Headers
fs.writeFileSync(
  path.join(OUT, '_headers'),
  `/*
  X-Content-Type-Options: nosniff
  X-Frame-Options: DENY
  Referrer-Policy: strict-origin-when-cross-origin
  Permissions-Policy: camera=(), microphone=(), geolocation=(), interest-cohort=()
  Cross-Origin-Opener-Policy: same-origin

/_next/static/*
  Cache-Control: public, max-age=31536000, immutable
`,
)
fs.writeFileSync(path.join(OUT, '_redirects'), `/docs /docs/quickstart/ 302\n/github https://github.com/AshwinSathian/weir 302\n`)

// 3. Links
const ids = new Map()
const read = (f) => fs.readFileSync(f, 'utf8')
const idsOf = (f) => {
  if (!ids.has(f)) ids.set(f, new Set([...read(f).matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])))
  return ids.get(f)
}
const errors = []
for (const file of pages) {
  const from = '/' + path.relative(OUT, file).replace(/index\.html$/, '')
  for (const m of read(file).matchAll(/<a\s[^>]*href="([^"]+)"/g)) {
    const href = m[1].replace(/&amp;/g, '&')
    // A link into this repository on GitHub must name a file that exists in the working tree.
    const gh = /^https:\/\/github\.com\/AshwinSathian\/weir\/(?:blob|tree|edit)\/main\/([^#?]*)/.exec(href)
    if (gh && !fs.existsSync(path.join(OUT, '../..', decodeURIComponent(gh[1])))) errors.push(`${from}: GitHub link to a file not in the repository: ${gh[1]}`)
    if (/^(https?:|mailto:)/.test(href)) continue
    const [p, frag] = href.split('#')
    let target = file
    if (p) {
      if (!p.startsWith('/')) { errors.push(`${from}: relative link ${href}`); continue }
      const t = path.join(OUT, p)
      target = fs.existsSync(t) && fs.statSync(t).isFile() ? t : path.join(t, 'index.html')
      if (!fs.existsSync(target)) { errors.push(`${from}: broken link ${href}`); continue }
      if (!p.endsWith('/') && target.endsWith('index.html')) errors.push(`${from}: link ${href} needs a trailing slash`)
    }
    if (frag && target.endsWith('.html') && !idsOf(target).has(decodeURIComponent(frag))) errors.push(`${from}: missing anchor ${href}`)
  }
}
if (errors.length) {
  console.error(errors.join('\n'))
  console.error(`postbuild: ${errors.length} broken links`)
  process.exit(1)
}
console.log(`postbuild: CSP on ${pages.length} pages, internal and repository links ok`)
