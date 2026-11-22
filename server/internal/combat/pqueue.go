// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

// priorityQueue is .NET's PriorityQueue<TElement, long>, step for step: a 4-ary min-heap that
// does not keep insertion order among equal priorities. The C# buff timers used it, so elements
// due at the same time come out in the same order here.
type priorityQueue[T any] struct {
	nodes []pqNode[T]
}

type pqNode[T any] struct {
	elem     T
	priority int64
}

const pqArity = 4

func (q *priorityQueue[T]) Len() int { return len(q.nodes) }

func (q *priorityQueue[T]) Enqueue(e T, priority int64) {
	q.nodes = append(q.nodes, pqNode[T]{})
	q.moveUp(pqNode[T]{e, priority}, len(q.nodes)-1)
}

// Peek is the root, false when empty.
func (q *priorityQueue[T]) Peek() (T, int64, bool) {
	if len(q.nodes) == 0 {
		var zero T
		return zero, 0, false
	}
	return q.nodes[0].elem, q.nodes[0].priority, true
}

func (q *priorityQueue[T]) Dequeue() T {
	e := q.nodes[0].elem
	last := len(q.nodes) - 1
	node := q.nodes[last]
	q.nodes = q.nodes[:last]
	if last > 0 {
		q.moveDown(node, 0)
	}
	return e
}

func (q *priorityQueue[T]) moveUp(node pqNode[T], i int) {
	for i > 0 {
		parent := (i - 1) >> 2
		if node.priority < q.nodes[parent].priority {
			q.nodes[i] = q.nodes[parent]
			i = parent
		} else {
			break
		}
	}
	q.nodes[i] = node
}

func (q *priorityQueue[T]) moveDown(node pqNode[T], i int) {
	size := len(q.nodes)
	for {
		c := (i << 2) + 1
		if c >= size {
			break
		}
		minChild, minIdx := q.nodes[c], c
		upper := min(c+pqArity, size)
		for c++; c < upper; c++ {
			if q.nodes[c].priority < minChild.priority {
				minChild, minIdx = q.nodes[c], c
			}
		}
		if node.priority <= minChild.priority {
			break
		}
		q.nodes[i] = minChild
		i = minIdx
	}
	q.nodes[i] = node
}
