package weir

import "context"

// GoBackground exposes goBackground to external tests.
func GoBackground(e *Engine, f func(context.Context)) { e.goBackground(f) }
