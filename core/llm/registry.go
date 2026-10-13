package llm

import (
	"sort"
	"sync"
)

// Registry is the in-memory model table. Replace is called on startup and after
// every console write to models or credentials. ponytail: single-instance only;
// multiple replicas would need a reload signal (e.g. Postgres LISTEN/NOTIFY).
type Registry struct {
	mu        sync.RWMutex
	byName    map[string]*Model
	providers map[string]Provider
}

func NewRegistry(providers map[string]Provider) *Registry {
	return &Registry{byName: map[string]*Model{}, providers: providers}
}

func (r *Registry) Replace(models []*Model) {
	m := make(map[string]*Model, len(models))
	for _, x := range models {
		m[x.Name] = x
	}
	r.mu.Lock()
	r.byName = m
	r.mu.Unlock()
}

func (r *Registry) Get(name string) (*Model, Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byName[name]
	if !ok {
		return nil, nil, false
	}
	p, ok := r.providers[m.Provider]
	return m, p, ok
}

func (r *Registry) List() []*Model {
	r.mu.RLock()
	out := make([]*Model, 0, len(r.byName))
	for _, m := range r.byName {
		out = append(out, m)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
