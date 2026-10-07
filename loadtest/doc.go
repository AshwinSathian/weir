// Package loadtest holds the real-time load and adversarial scenarios of
// docs/07-testing-strategy.md §9. Everything is behind the `load` build tag:
// run it with `make load`. WEIR_LOAD_SCALE (default 1) shortens the long
// phases for a smoke run; thresholds are asserted at any scale.
package loadtest
