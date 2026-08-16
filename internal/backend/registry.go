package backend

import (
	"fmt"
	"sort"
	"sync"
)

// DefaultName is the backend an instance runs when its config does not name
// one.
const DefaultName = "claude-code"

// RemovedName is the backend this binary no longer implements. It is named
// rather than merely absent because every instance created before the config
// carried a backend field resolves to it: those documents are still on disk,
// and an operator whose box stops resolving is owed the reason and the way
// forward rather than a list of names that does not include the one they had.
const RemovedName = "hermes"

var (
	mu       sync.RWMutex
	registry = map[string]Backend{}
)

// Register makes b available to Lookup under its identity name. The composition
// root calls it; implementations do not register themselves, so importing one
// has no effect on what an instance can select.
//
// It panics on an empty or duplicate name: both are programmer errors in wiring
// that must not reach an operator as a runtime surprise.
func Register(b Backend) {
	name := b.Identity().Name
	if name == "" {
		panic("backend: registered a backend with no name")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic("backend: duplicate registration for " + name)
	}
	registry[name] = b
}

// Lookup resolves a backend by name. An unknown name names the known ones back:
// an operator who mistyped a backend has to be told what the alternatives are,
// and a config document written by a newer Torio must fail loudly rather than
// fall back to the default and run the wrong agent.
func Lookup(name string) (Backend, error) {
	if name == "" {
		name = DefaultName
	}
	mu.RLock()
	defer mu.RUnlock()
	b, ok := registry[name]
	if !ok {
		if name == RemovedName {
			return nil, fmt.Errorf(
				"backend %q was removed; this box cannot be reached as it is. "+
					"Create a box on a supported backend with `torio vm init --backend %s`, "+
					"or rebind this instance from the hub. Known backends: %v",
				RemovedName, DefaultName, namesLocked())
		}
		return nil, fmt.Errorf("unknown backend %q; known backends: %v", name, namesLocked())
	}
	return b, nil
}

// Names lists the registered backends in sorted order. The hub's rebind
// chooser offers exactly this set (ADR-0021), so what an operator can pick
// and what Lookup accepts cannot disagree.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return namesLocked()
}

func namesLocked() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
