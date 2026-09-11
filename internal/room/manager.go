package room

import (
	"context"
	"sync"
)

type Manager struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

func NewManager() *Manager { return &Manager{rooms: map[string]*Room{}} }
func (m *Manager) Create(ctx context.Context, id string, a, b string, out func(string, []byte)) *Room {
	r := New(id, a, b, out)
	m.mu.Lock()
	m.rooms[id] = r
	m.mu.Unlock()
	go func() {
		r.Run(ctx)
		m.Remove(id)
	}()
	return r
}
func (m *Manager) Get(id string) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rooms[id]
	return r, ok
}
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	r, ok := m.rooms[id]
	delete(m.rooms, id)
	m.mu.Unlock()
	if ok {
		r.Close()
	}
}

func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}
