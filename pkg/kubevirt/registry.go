package kubevirt

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// InClusterContext is the synthetic context name used when the app runs
// in-cluster (no kubeconfig). In that mode the registry has exactly one
// entry and KinD create/delete are disabled.
const InClusterContext = "in-cluster"

// ContextInfo is a single entry in the cluster list exposed to the UI.
type ContextInfo struct {
	Name    string `json:"context"`
	Server  string `json:"server"`
	Current bool   `json:"current"`
	IsKind  bool   `json:"is_kind"` // context name has the `kind-` prefix → eligible for delete
	InCluster bool `json:"in_cluster"`
}

// Registry owns the active-cluster state for the server. Handlers get
// their Client via Active() per-request — never stash it.
//
// Contexts are enumerated fresh from ~/.kube/config on every List() so
// `kind create` (which mutates kubeconfig) is picked up without restart.
// Client objects are built lazily on first Select() and cached by
// context name; Invalidate() drops a cached entry (used after delete).
type Registry struct {
	logger         *slog.Logger
	kubeconfigPath string // explicit override; empty = default rules
	namespace      string

	mu        sync.RWMutex
	clients   map[string]Client
	active    string
	inCluster bool // true when the initial client was built from in-cluster config
}

// NewRegistry boots the registry and eagerly builds a Client for the
// initial active context (in-cluster if applicable, else the kubeconfig's
// current-context). Other contexts are built lazily on Select.
func NewRegistry(logger *slog.Logger, kubeconfigPath, namespace string) (*Registry, error) {
	r := &Registry{
		logger:         logger,
		kubeconfigPath: kubeconfigPath,
		namespace:      namespace,
		clients:        make(map[string]Client),
	}

	if kubeconfigPath == "" {
		if restCfg, err := rest.InClusterConfig(); err == nil {
			ns := namespace
			if ns == "" {
				ns = inferNamespace("")
			}
			c, err := buildClientFromRest(logger, restCfg, ns, InClusterContext, "in-cluster")
			if err != nil {
				return nil, err
			}
			r.clients[InClusterContext] = c
			r.active = InClusterContext
			r.inCluster = true
			return r, nil
		}
	}

	current := r.readCurrentContext()
	if current == "" {
		// kind deletes the current-context entry when removing its owning
		// cluster without picking a fallback, leaving the file in a
		// "has contexts, no current" state that would otherwise block
		// startup. Pick the alphabetically-first context instead.
		cfg, err := r.loadKubeconfig()
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("no contexts in kubeconfig %s", r.resolvedKubeconfigPath())
		}
		sort.Strings(names)
		current = names[0]
		logger.Warn("kubeconfig has no current-context; falling back to first entry", "context", current, "path", r.resolvedKubeconfigPath())
	}
	c, err := r.buildForContext(current)
	if err != nil {
		return nil, err
	}
	r.clients[current] = c
	r.active = current
	return r, nil
}

// Active returns the Client for the currently-selected context. Never
// nil after successful NewRegistry.
func (r *Registry) Active() Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clients[r.active]
}

// ActiveContext returns the name of the active context.
func (r *Registry) ActiveContext() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// InCluster reports whether this registry is backed by in-cluster config
// (as opposed to a kubeconfig file). In-cluster mode disables multi-cluster
// operations — KinD create/delete and context-switching are meaningless.
func (r *Registry) InCluster() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.inCluster
}

// List enumerates contexts from the kubeconfig file (re-read on every
// call so newly-created KinD contexts show up without restart).
func (r *Registry) List() ([]ContextInfo, error) {
	r.mu.RLock()
	active := r.active
	inCluster := r.inCluster
	r.mu.RUnlock()

	if inCluster {
		return []ContextInfo{{
			Name:      InClusterContext,
			Current:   true,
			InCluster: true,
		}}, nil
	}

	cfg, err := r.loadKubeconfig()
	if err != nil {
		return nil, err
	}
	out := make([]ContextInfo, 0, len(cfg.Contexts))
	for name, ctx := range cfg.Contexts {
		server := ""
		if cluster, ok := cfg.Clusters[ctx.Cluster]; ok {
			server = cluster.Server
		}
		out = append(out, ContextInfo{
			Name:    name,
			Server:  server,
			Current: name == active,
			IsKind:  strings.HasPrefix(name, "kind-"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Select switches the active context. The Client is built lazily on
// first select and cached thereafter. A Select to the already-active
// context is a no-op.
func (r *Registry) Select(contextName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inCluster {
		return fmt.Errorf("running in-cluster — context switching is disabled")
	}
	if contextName == r.active {
		return nil
	}
	if _, ok := r.clients[contextName]; !ok {
		// Verify the context exists in kubeconfig before building.
		cfg, err := r.loadKubeconfig()
		if err != nil {
			return err
		}
		if _, ok := cfg.Contexts[contextName]; !ok {
			return fmt.Errorf("context %q not found in kubeconfig", contextName)
		}
		c, err := r.buildForContext(contextName)
		if err != nil {
			return err
		}
		r.clients[contextName] = c
	}
	r.active = contextName
	return nil
}

// Invalidate drops the cached Client for a context (used after a context
// is deleted, e.g. `kind delete cluster`). If the invalidated context
// was active, callers should Select() a fallback context next.
func (r *Registry) Invalidate(contextName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, contextName)
}

// KubeconfigPath returns the path the registry reads. Useful for kind
// subprocesses that need `--kubeconfig` to be explicit.
func (r *Registry) KubeconfigPath() string {
	return r.resolvedKubeconfigPath()
}

func (r *Registry) buildForContext(contextName string) (Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if r.kubeconfigPath != "" {
		loadingRules.ExplicitPath = r.kubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	clientCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	restCfg, err := clientCfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config for %q: %w", contextName, err)
	}
	ns := r.namespace
	if ns == "" {
		if n, _, err := clientCfg.Namespace(); err == nil {
			ns = n
		}
	}
	return buildClientFromRest(r.logger, restCfg, ns, contextName, "kubeconfig:"+r.resolvedKubeconfigPath())
}

func (r *Registry) loadKubeconfig() (*clientcmdapi.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if r.kubeconfigPath != "" {
		loadingRules.ExplicitPath = r.kubeconfigPath
	}
	cfg, err := loadingRules.Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return cfg, nil
}

func (r *Registry) readCurrentContext() string {
	cfg, err := r.loadKubeconfig()
	if err != nil {
		return ""
	}
	return cfg.CurrentContext
}

// resolvedKubeconfigPath returns the first path the loading rules would
// try, for display and `--kubeconfig` CLI args. Empty only when neither
// an explicit path, $KUBECONFIG, nor ~/.kube/config exists.
func (r *Registry) resolvedKubeconfigPath() string {
	if r.kubeconfigPath != "" {
		return r.kubeconfigPath
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		// KUBECONFIG can be colon-separated; kind writes to the first entry.
		if parts := strings.Split(env, string(os.PathListSeparator)); len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".kube", "config")
	}
	return ""
}
