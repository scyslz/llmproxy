package proxy

import (
	"sync"
)

// Semaphore 限制 provider 的并发转发数；capacity<=0 表示不限制。
type Semaphore struct {
	capacity int
	ch       chan struct{}
}

// NewSemaphore 构造信号量。
func NewSemaphore(capacity int) *Semaphore {
	if capacity <= 0 {
		return &Semaphore{capacity: 0}
	}
	return &Semaphore{capacity: capacity, ch: make(chan struct{}, capacity)}
}

// Acquire 占用一个槽位；capacity<=0 时立即返回。
func (s *Semaphore) Acquire() {
	if s.capacity <= 0 {
		return
	}
	s.ch <- struct{}{}
}

// Release 释放一个槽位。
func (s *Semaphore) Release() {
	if s.capacity <= 0 {
		return
	}
	<-s.ch
}

var (
	semMu      sync.Mutex
	semRegistry = make(map[string]*Semaphore)
)

// globalSemaphore 返回 provider 维度的全局信号量；concurrency<=0 返回 nil。
func globalSemaphore(providerID string, concurrency int) *Semaphore {
	if concurrency <= 0 {
		return nil
	}
	semMu.Lock()
	defer semMu.Unlock()
	if s, ok := semRegistry[providerID]; ok {
		return s
	}
	s := NewSemaphore(concurrency)
	semRegistry[providerID] = s
	return s
}