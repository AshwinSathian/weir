package weir

import "context"

// GoBackground exposes goBackground to external tests.
func GoBackground(e *Engine, f func(context.Context)) { e.goBackground(f) }

// Flights returns the number of flights in e's coalescing table.
func Flights(e *Engine) int { return e.flights.Len() }
