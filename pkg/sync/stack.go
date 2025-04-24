package sync

import "sync"

type Stack[T any] struct {
	mu  sync.Mutex
	arr []T
}

func NewStack[T any]() *Stack[T] {
	return &Stack[T]{
		arr: make([]T, 0),
	}
}

func (s *Stack[T]) Push(t T) {
	s.mu.Lock()
	s.arr = append(s.arr, t)
	s.mu.Unlock()
}

func (s *Stack[T]) Pop() (t T) {
	s.mu.Lock()
	if len(s.arr) == 0 {
		s.mu.Unlock()
		return t
	}
	res := s.arr[len(s.arr)-1]
	s.arr = s.arr[:len(s.arr)-1]
	s.mu.Unlock()
	return res
}
