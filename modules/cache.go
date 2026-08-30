package modules

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// sharedCache is the process-wide cache shared by all modules. It lets
// modules reuse cluster-wide lists (nodes, pods, ...) fetched by another
// module instead of issuing the same API calls again.
var sharedCache = NewCache()

// Cache lazily fetches and caches cluster-wide lists.
type Cache struct {
	mu       sync.Mutex
	nodes    *corev1.NodeList
	nodesErr error
	pods     *corev1.PodList
	podsErr  error
}

func NewCache() *Cache {
	return &Cache{}
}

// Nodes returns the cached node list, fetching it once on first use.
func (c *Cache) Nodes(ctx context.Context) (*corev1.NodeList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.nodes == nil && c.nodesErr == nil {
		clientset, err := newClientset()
		if err != nil {
			c.nodesErr = err
			return nil, err
		}
		c.nodes, c.nodesErr = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	}
	return c.nodes, c.nodesErr
}

// Pods returns the cached pod list across all namespaces, fetching it once on
// first use.
func (c *Cache) Pods(ctx context.Context) (*corev1.PodList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pods == nil && c.podsErr == nil {
		clientset, err := newClientset()
		if err != nil {
			c.podsErr = err
			return nil, err
		}
		c.pods, c.podsErr = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	}
	return c.pods, c.podsErr
}
