// RELEASED=false shows the pre-release banner and the rc install lines.
export const RELEASED = true
export const VERSION = 'v1.2.0'
export const RC_VERSION = 'v1.2.0-rc.1'
export const SHOWN_VERSION = RELEASED ? VERSION : RC_VERSION

export const MODULE = 'github.com/AshwinSathian/weir'
export const MODULES = ['', '/observe/prom', '/store/valkey', '/caddy'].map((m) => MODULE + m)
export const REPO = 'https://github.com/AshwinSathian/weir'
export const SITE = 'https://weir.ashwinsathian.com'
export const GO_MIN = '1.26.0'
export const GO_DEPLOY = '1.27.3'
export const CADDY_VERSION = 'v2.11.7'
