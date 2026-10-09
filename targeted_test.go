package weir_test

import (
	"testing"
	"testing/synctest"
	"time"
)

func runRows(t *testing.T, rows []rfcRow) {
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { runRFCRow(t, row) })
		})
	}
}

// FR-TCC-1, FR-TCC-2; RFC 9213 §2.1 and §2.2.
func TestTargetedFieldPrecedence(t *testing.T) {
	runRows(t, []rfcRow{
		{sec: "9213 §2", name: "CDN-Cache-Control overrides Cache-Control and Expires", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=10", "Cdn-Cache-Control", "max-age=100", "Expires", rfcDate(5*time.Second)), calls: 1},
			{after: 50 * time.Second, calls: 1, body: "a"},
			{after: 51 * time.Second, calls: 2},
		}},
		{sec: "9213 §2", name: "Weir-Cache-Control wins over CDN-Cache-Control", steps: []rfcStep{
			{origin: bh(200, "a", "Weir-Cache-Control", "max-age=100", "Cdn-Cache-Control", "max-age=10"), calls: 1},
			{after: 50 * time.Second, calls: 1},
		}},
		{sec: "9213 §2", name: "targeted field without freshness drops Expires", steps: []rfcStep{
			{origin: bh(200, "a", "Cdn-Cache-Control", "public", "Expires", rfcDate(100*time.Second)), calls: 1},
			{after: 5 * time.Second, calls: 2},
		}},
		{sec: "9213 §2.1", name: "unparsable field is ignored", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=100", "Cdn-Cache-Control", "max-age=(1"), calls: 1},
			{after: 50 * time.Second, calls: 1},
		}},
		{sec: "9213 §2.1", name: "empty field is ignored", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=100", "Cdn-Cache-Control", ""), calls: 1},
			{after: 50 * time.Second, calls: 1},
		}},
		{sec: "9213 §2.1", name: "decimal max-age makes the field invalid, next target applies", steps: []rfcStep{
			{origin: bh(200, "a", "Weir-Cache-Control", "max-age=1.5", "Cdn-Cache-Control", "max-age=100"), calls: 1},
			{after: 50 * time.Second, calls: 1},
		}},
		{sec: "9213 §2.1", name: "negative s-maxage makes the field invalid, Cache-Control applies", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=100", "Cdn-Cache-Control", "s-maxage=-1"), calls: 1},
			{after: 50 * time.Second, calls: 1},
		}},
		// FR-TCC-5: no Age or Date rewrite to hide the longer lifetime.
		{sec: "9213 §2.3", name: "Age and Date describe the stored response, not the targeted lifetime", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=10", "Cdn-Cache-Control", "max-age=100", "Date", rfcDate(0)), calls: 1},
			{after: 20 * time.Second, calls: 1, resp: []string{"Age", "20", "Date", rfcDate(0)}},
		}},
		{sec: "9213 §2", name: "targeted stale-while-revalidate sets the window", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "max-age=10", "Cdn-Cache-Control", "max-age=10, stale-while-revalidate=30"), calls: 1},
			{after: 20 * time.Second, calls: 2, body: "a"},
		}},
	})
}

// FR-TCC-3, T-34.
func TestTargetedFieldKeepsPrivate(t *testing.T) {
	notStored := func(cc, targeted string) rfcRow {
		return rfcRow{name: cc + " | " + targeted, steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", cc, "Cdn-Cache-Control", targeted), calls: 1},
			{calls: 2},
		}}
	}
	runRows(t, []rfcRow{
		notStored("private", "max-age=100"),
		notStored("no-store", "max-age=100"),
		notStored("no-cache", "max-age=100"),
		notStored("max-age=100", "max-age=100, private"),
		notStored("max-age=100", "max-age=100, no-store"),
		notStored("max-age=100", "max-age=100, private=?0"),
		notStored("no-store", "max-age=100, must-understand"),
		{name: "Weir-Cache-Control keeps Cache-Control private", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "private", "Weir-Cache-Control", "max-age=100"), calls: 1},
			{calls: 2},
		}},
		{name: "Weir-Cache-Control keeps Cache-Control no-cache", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "no-cache", "Weir-Cache-Control", "max-age=100"), calls: 1},
			{calls: 2},
		}},
		{name: "must-understand in both fields lifts no-store", steps: []rfcStep{
			{origin: bh(200, "a", "Cache-Control", "no-store, must-understand", "Cdn-Cache-Control", "max-age=100, must-understand"), calls: 1},
			{calls: 1, body: "a"},
		}},
	})
}

// FR-TCC-4.
func TestWeirCacheControlStripped(t *testing.T) {
	runRows(t, []rfcRow{
		{name: "miss, hit and conditional 304 omit Weir-Cache-Control and keep CDN-Cache-Control", steps: []rfcStep{
			{origin: bh(200, "a", "Weir-Cache-Control", "max-age=100", "Cdn-Cache-Control", "max-age=5", "Etag", `"v"`), calls: 1,
				resp: []string{"Weir-Cache-Control", "", "Cdn-Cache-Control", "max-age=5"}},
			{after: 50 * time.Second, calls: 1, resp: []string{"Weir-Cache-Control", "", "Cdn-Cache-Control", "max-age=5"}, cacheStatus: "Weir; hit; ttl=50"},
			{after: 1 * time.Second, hdr: []string{"If-None-Match", `"v"`}, status: 304, calls: 1, resp: []string{"Weir-Cache-Control", ""}},
		}},
		{name: "an unstored response also omits it", steps: []rfcStep{
			{origin: bh(200, "a", "Weir-Cache-Control", "no-store"), calls: 1, resp: []string{"Weir-Cache-Control", ""}},
		}},
	})
}

// FR-TCC-2, FR-TCC-4, T-8, T-34: a 304 from the origin replaces the stored
// targeted field, so freshness follows it; a targeted public is explicit
// freshness for an Authorization request, like public in Cache-Control.
func TestTargetedFieldRevalidationAndAuthorization(t *testing.T) {
	runRows(t, []rfcRow{
		{name: "origin 304 with a new Weir-Cache-Control extends freshness", steps: []rfcStep{
			{origin: bh(200, "a", "Weir-Cache-Control", "max-age=10", "Etag", `"v"`), calls: 1},
			{after: 11 * time.Second, origin: bh(304, "", "Weir-Cache-Control", "max-age=100", "Etag", `"v"`), calls: 2, body: "a",
				resp: []string{"Weir-Cache-Control", ""}},
			{after: 50 * time.Second, calls: 2, body: "a"},
		}},
		{name: "Authorization request with Cdn-Cache-Control public is stored", steps: []rfcStep{
			{origin: bh(200, "a", "Cdn-Cache-Control", "public, max-age=100"), hdr: []string{"Authorization", "Bearer x"}, calls: 1},
			{hdr: []string{"Authorization", "Bearer x"}, calls: 1, body: "a"},
		}},
	})
}
