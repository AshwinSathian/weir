---
title: About
description: Where Weir came from, and why it is called that.
---

## Three gaps

Weir began with a caching layer that had three weaknesses, and a suspicion that they weren't unusual.

It could not survive a hard reset. Flush the cache and every request after that was a cold miss, all of them at once, all of them headed for the origin. It had no defence against flooding either: when requests arrived faster than the origin could regenerate entries, nothing held them back. And it had no TTL jitter, so entries that were stored in a batch expired in a batch, and the stampede came back on a schedule instead of happening once.

None of this is exotic. Is it solved elsewhere? Partly. Request coalescing is common, stale-while-revalidate is a standard, and the security research on cache poisoning has been public for years. But coalescing only helps when requests share a key, most caches still leave jitter and a cross-key budget to whoever operates them, and the defences that do exist tend to live in different tools. So the same three gaps are still open in a lot of what people actually run in production.

I wanted to close them properly, once, as something reusable: partly because I'd rather understand this space than route around it, and partly because the gap looked real. Weir is the result: RFC 9111 caching, with the defences in one Go library and a written threat model beside them. Whether the combination earns its place is for the people who run it to say.

## The name

A weir is a low dam built across a river. It doesn't stop the water; it regulates how fast the water gets through. That is the job here. Traffic reaches the origin at a rate the origin can survive, whatever the cache happens to be doing.

The vocabulary was already halfway there. Caches flood, stampede and get poisoned, so a water-control name was always going to win. Sluice, Levee and Cistern were on the list. Weir was the shortest, and its metaphor held up best: a levee blocks, a weir lets through.

## Who

Weir is written and maintained by [Ashwin Sathian](https://github.com/AshwinSathian). The code and this documentation are under Apache-2.0.

## For contributors

The repository carries more process than most: normative design documents with requirement IDs that the tests cite. They are in [`docs/`](https://github.com/AshwinSathian/weir/tree/main/docs), and [CONTRIBUTING.md](https://github.com/AshwinSathian/weir/blob/main/CONTRIBUTING.md) says which ones you need to read for a given change.
