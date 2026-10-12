import type { NextConfig } from 'next'

// Static files only: no server runtime on Cloudflare Pages.
const nextConfig: NextConfig = {
  output: 'export',
  trailingSlash: true,
  images: { unoptimized: true },
  reactStrictMode: true,
}

export default nextConfig
