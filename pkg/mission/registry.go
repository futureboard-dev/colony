package mission

import (
	"fmt"
	"sync"

	"github.com/jirateep/colony/pkg/config"
)

// NodeFactory creates a Node for a given agent ID and LLM config.
type NodeFactory func(agentID string, cfg config.LLMConfig) (Node, error)

// Registry maps role names to NodeFactory functions.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]NodeFactory
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]NodeFactory)}
}

// Register adds a factory for the given role name.
func (r *Registry) Register(role string, factory NodeFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[role] = factory
}

// Create instantiates a Node for the given role, agentID, and config.
func (r *Registry) Create(role, agentID string, cfg config.LLMConfig) (Node, error) {
	r.mu.RLock()
	factory, ok := r.factories[role]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown role %q: not registered", role)
	}
	return factory(agentID, cfg)
}

// DefaultRegistry is the package-level registry; roles register themselves via init().
var DefaultRegistry = NewRegistry()

// Register adds a factory to the default registry.
func Register(role string, factory NodeFactory) {
	DefaultRegistry.Register(role, factory)
}
