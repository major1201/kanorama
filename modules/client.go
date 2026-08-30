package modules

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeconfigPath string
	contextName    string
)

// SetClientConfig configures how newClientset resolves the kubeconfig. It
// must be called before running modules. An empty kubeconfigPath uses the
// default loading rules (KUBECONFIG env or ~/.kube/config); an empty
// contextName uses the current context.
func SetClientConfig(kubeconfig, context string) {
	kubeconfigPath = kubeconfig
	contextName = context
}

// newClientset builds a Kubernetes clientset. When neither --kubeconfig nor
// --context is configured it prefers in-cluster config and falls back to the
// default kubeconfig. If either option is set it always uses kubeconfig-based
// resolution.
func newClientset() (*kubernetes.Clientset, error) {
	config, err := newRestConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

// newRestConfig resolves the shared REST config using the same logic as
// newClientset, for clients other than *kubernetes.Clientset (e.g. dynamic).
func newRestConfig() (*rest.Config, error) {
	if kubeconfigPath == "" && contextName == "" {
		if config, err := rest.InClusterConfig(); err == nil {
			return config, nil
		}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		overrides,
	).ClientConfig()
}
