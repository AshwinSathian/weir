package memory

import (
	"sync/atomic"
	"time"

	"github.com/AshwinSathian/weir/store"
)

// Queue identifiers for node.queue.
const (
	queueSmall uint8 = iota
	queueMain
)

// node is one record in a shard (05 §5.3). Every field except freq is
// guarded by the shard's write lock; freq changes under the read lock.
type node struct {
	key        store.Key
	e          *store.Entry
	size       int64
	expires    time.Time     // e.Expires clamped by MaxRetention (E-11)
	fp         uint64        // ghost fingerprint of key
	freq       atomic.Uint32 // 0..3
	queue      uint8
	prev, next *node
}

func (n *node) expired(now time.Time) bool { return !now.Before(n.expires) }

// fifo is an intrusive doubly linked queue: push at the head, evict from the
// tail. bytes and len track its contents.
type fifo struct {
	head, tail *node
	bytes      int64
	len        int
}

func (q *fifo) push(n *node) {
	n.prev, n.next = nil, q.head
	if q.head != nil {
		q.head.prev = n
	} else {
		q.tail = n
	}
	q.head = n
	q.bytes += n.size
	q.len++
}

func (q *fifo) remove(n *node) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		q.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		q.tail = n.prev
	}
	n.prev, n.next = nil, nil
	q.bytes -= n.size
	q.len--
}
