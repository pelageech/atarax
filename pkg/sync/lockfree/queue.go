package lockfree

import (
	"sync/atomic"
	"unsafe"
)

// Queue is a simple, fast, and practical non-blocking queue.
//
// Fork from "go.chensl.me/lockfreequeue".
type Queue[T any] struct {
	head unsafe.Pointer
	tail unsafe.Pointer
	len  int64
}

type node[T any] struct {
	value T
	next  unsafe.Pointer
}

// NewQueue creates a queue with dummy node.
func NewQueue[T any]() *Queue[T] {
	node := unsafe.Pointer(new(node[T]))
	return &Queue[T]{
		head: node,
		tail: node,
	}
}

// Enqueue push back the given value v to queue.
func (q *Queue[T]) Enqueue(v T) {
	node := &node[T]{value: v}
	for {
		tail := load[T](&q.tail)
		next := load[T](&tail.next)
		if tail == load[T](&q.tail) {
			if next == nil {
				if cas(&tail.next, next, node) {
					cas(&q.tail, tail, node)
					inc(&q.len)
					return
				}
			} else {
				cas(&q.tail, tail, next)
			}
		}
	}
}

// Dequeue pop front a value from queue
func (q *Queue[T]) Dequeue() (v T, ok bool) {
	for {
		head := load[T](&q.head)
		tail := load[T](&q.tail)
		next := load[T](&head.next)
		if head == load[T](&q.head) {
			if head == tail {
				if next == nil {
					var zero T
					return zero, false
				}
				cas(&q.tail, tail, next)
			} else {
				v := next.value
				if cas(&q.head, head, next) {
					dec(&q.len)
					return v, true
				}
			}
		}
	}
}

func (q *Queue[T]) Len() int64 {
	return atomic.LoadInt64(&q.len)
}

func inc(i *int64) int64 {
	return atomic.AddInt64(i, 1)
}

func dec(i *int64) int64 {
	return atomic.AddInt64(i, -1)
}

func load[T any](p *unsafe.Pointer) *node[T] {
	return (*node[T])(atomic.LoadPointer(p))
}

func cas[T any](p *unsafe.Pointer, old, new *node[T]) bool {
	return atomic.CompareAndSwapPointer(p,
		unsafe.Pointer(old), unsafe.Pointer(new))
}
