'use client'

import { useEffect, useRef, useState } from 'react'

const store = {
  get: (k: string) => { try { return localStorage.getItem(k) } catch { return null } },
  set: (k: string, v: string) => { try { localStorage.setItem(k, v) } catch { /* private mode */ } },
}

export function ThemeToggle() {
  const flip = () => {
    const root = document.documentElement
    root.dataset.theme = root.dataset.theme === 'light' ? 'dark' : 'light'
    store.set('theme', root.dataset.theme)
  }
  return (
    <button className="icon-btn" onClick={flip} aria-label="Switch between light and dark theme">
      <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
        <circle cx="9" cy="9" r="6.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
        <path d="M9 2.5a6.5 6.5 0 0 0 0 13z" fill="currentColor" />
      </svg>
    </button>
  )
}

export function MenuButton() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const nav = document.getElementById('mobile-nav')
    if (nav) nav.hidden = !open
  }, [open])
  return (
    <button className="icon-btn menu-btn" aria-expanded={open} aria-controls="mobile-nav" aria-label="Menu" onClick={() => setOpen(!open)}>
      <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
        <path d={open ? 'M4 4l10 10M14 4L4 14' : 'M2 5h14M2 9h14M2 13h14'} stroke="currentColor" strokeWidth="1.5" fill="none" />
      </svg>
    </button>
  )
}

type PagefindUI = new (o: { element: string; showSubResults: boolean; showImages: boolean }) => unknown

/** Pagefind loads only when the reader opens search, so no page pays for it. */
export function Search() {
  const ref = useRef<HTMLDialogElement>(null)
  const loaded = useRef(false)
  const open = async () => {
    ref.current?.showModal()
    if (!loaded.current) {
      loaded.current = true
      const css = document.createElement('link')
      css.rel = 'stylesheet'
      css.href = '/pagefind/pagefind-ui.css'
      document.head.append(css)
      await new Promise<void>((done, fail) => {
        const s = document.createElement('script')
        s.src = '/pagefind/pagefind-ui.js'
        s.onload = () => done()
        s.onerror = () => fail(new Error('search index not built'))
        document.head.append(s)
      }).then(
        () => new (window as unknown as { PagefindUI: PagefindUI }).PagefindUI({ element: '#search-ui', showSubResults: true, showImages: false }),
        () => { document.getElementById('search-ui')!.textContent = 'Search is available on the built site.' },
      )
    }
    ref.current?.querySelector('input')?.focus()
  }
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') { e.preventDefault(); void open() }
    }
    addEventListener('keydown', key)
    return () => removeEventListener('keydown', key)
  })
  return (
    <>
      <button className="icon-btn search-btn" onClick={() => void open()} aria-label="Search the site">
        <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><circle cx="6" cy="6" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.5" /><path d="M9.5 9.5L13 13" stroke="currentColor" strokeWidth="1.5" /></svg>
        <span>Search</span>
      </button>
      <dialog ref={ref} className="search" aria-label="Search" onClick={(e) => { if (e.target === ref.current) ref.current?.close() }}>
        <div id="search-ui" />
      </dialog>
    </>
  )
}

/** Progressive enhancement for rendered Markdown: copy buttons, tabs, table filter. */
export function Enhance() {
  useEffect(() => {
    for (const pre of document.querySelectorAll<HTMLElement>('.prose pre, pre[data-copy]')) {
      if (pre.parentElement?.classList.contains('codebox')) continue
      const box = document.createElement('div')
      box.className = 'codebox'
      pre.replaceWith(box)
      const btn = document.createElement('button')
      btn.className = 'copy'
      btn.type = 'button'
      btn.textContent = 'Copy'
      btn.onclick = async () => {
        await navigator.clipboard.writeText(pre.innerText)
        btn.textContent = 'Copied'
        setTimeout(() => (btn.textContent = 'Copy'), 1500)
      }
      box.append(pre, btn)
    }
    for (const tabs of document.querySelectorAll<HTMLElement>('[data-tabs]')) {
      if (tabs.querySelector('[role=tablist]')) continue
      const names = (tabs.dataset.tabs ?? '').split(',')
      const panels = [...tabs.querySelectorAll<HTMLElement>(':scope > section')]
      const list = document.createElement('div')
      list.setAttribute('role', 'tablist')
      const group = tabs.dataset.group ?? 'tabs'
      const show = (i: number) => {
        panels.forEach((p, j) => (p.hidden = j !== i))
        list.querySelectorAll('button').forEach((b, j) => b.setAttribute('aria-selected', String(j === i)))
        store.set('tab:' + group, names[i])
      }
      names.forEach((name, i) => {
        const b = document.createElement('button')
        b.type = 'button'
        b.setAttribute('role', 'tab')
        b.textContent = name
        b.onclick = () => show(i)
        list.append(b)
        panels[i]?.setAttribute('role', 'tabpanel')
        panels[i]?.setAttribute('aria-label', name)
      })
      tabs.prepend(list)
      show(Math.max(0, names.indexOf(store.get('tab:' + group) ?? '')))
    }
    for (const input of document.querySelectorAll<HTMLInputElement>('input[data-filter]')) {
      const rows = [...document.querySelectorAll<HTMLElement>(`${input.dataset.filter} tbody tr`)]
      input.oninput = () => {
        const q = input.value.trim().toLowerCase()
        for (const r of rows) r.hidden = q !== '' && !r.textContent!.toLowerCase().includes(q)
      }
    }
  }, [])
  return null
}

export type Line = { kind: 'in' | 'out' | 'hl' | 'note'; text: string }

/** The hero transcript: real output, typed out once, replayable, always copyable text. */
export function Transcript({ lines }: { lines: Line[] }) {
  const [n, setN] = useState(lines.length)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const play = () => {
    clearTimeout(timer.current)
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) return setN(lines.length)
    let i = 0
    const step = () => {
      i++
      setN(i)
      if (i < lines.length) timer.current = setTimeout(step, lines[i].kind === 'in' ? 700 : 90)
    }
    setN(0)
    timer.current = setTimeout(step, 250)
  }
  useEffect(() => () => clearTimeout(timer.current), [])
  return (
    <div className="term">
      <div className="term-bar">
        <span className="label">weirproxy in front of an origin</span>
        <button className="text-btn" onClick={play}>Replay</button>
      </div>
      <pre aria-label="Terminal transcript">
        {lines.slice(0, n).map((l, i) => (
          <span key={i} className={l.kind}>{l.text + '\n'}</span>
        ))}
      </pre>
    </div>
  )
}
