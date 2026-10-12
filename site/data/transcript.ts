import type { Line } from '@/components/client'

// Captured 2026-10-12 from examples/weirproxy at v1.0.0 in front of an origin
// that answers "Cache-Control: max-age=60". Regenerate with scripts/transcript.sh.
export const transcript: Line[] = [
  { kind: 'in', text: '$ weirproxy -listen :8080 -origin http://localhost:9000' },
  { kind: 'in', text: '$ curl -si localhost:8080/' },
  { kind: 'out', text: 'HTTP/1.1 200 OK' },
  { kind: 'out', text: 'Cache-Control: max-age=60' },
  { kind: 'hl', text: 'Cache-Status: Weir; fwd=uri-miss; fwd-status=200; stored' },
  { kind: 'out', text: '' },
  { kind: 'out', text: 'hello from the origin' },
  { kind: 'note', text: '# 13 seconds later' },
  { kind: 'in', text: '$ curl -si localhost:8080/' },
  { kind: 'out', text: 'HTTP/1.1 200 OK' },
  { kind: 'out', text: 'Age: 13' },
  { kind: 'out', text: 'Cache-Control: max-age=60' },
  { kind: 'hl', text: 'Cache-Status: Weir; hit; ttl=43' },
  { kind: 'out', text: '' },
  { kind: 'out', text: 'hello from the origin' },
]
