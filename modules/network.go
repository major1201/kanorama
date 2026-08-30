package modules

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Network struct {
	ModuleAbstract

	cni          []string
	podCIDRs     []string
	serviceCIDRs []string
	dnsDomain    string
}

func (m Network) Name() string {
	return "Network"
}

func (m Network) ID() string {
	return strings.ToLower(m.Name())
}

func (m Network) Visible() bool {
	return true
}

func (m Network) EnableByDefault() bool {
	return true
}

// cniNodeAnnotationPrefixes maps well-known node annotation prefixes to the
// CNI implementation that owns them.
var cniNodeAnnotationPrefixes = map[string]string{
	"projectcalico.org/":        "Calico",
	"k8s.ovn.org/":              "OVN-Kubernetes",
	"flannel.alpha.coreos.com/": "Flannel",
	"io.cilium.network.":        "Cilium",
	"cilium.io/":                "Cilium",
}

// cniDaemonSetNames maps well-known CNI DaemonSet names to the CNI
// implementation they install.
var cniDaemonSetNames = map[string]string{
	"calico-node":         "Calico",
	"kube-flannel-ds":     "Flannel",
	"kube-flannel":        "Flannel",
	"cilium":              "Cilium",
	"ovnkube-node":        "OVN-Kubernetes",
	"ovn-kubernetes-node": "OVN-Kubernetes",
	"kube-router":         "Kube-router",
	"weave-net":           "Weave Net",
	"antrea-agent":        "Antrea",
	"aws-node":            "Amazon VPC CNI",
	"azure-cni":           "Azure CNI",
	"terway":              "Terway",
	"terway-eni":          "Terway",
	"terway-eniip":        "Terway",
	"kindnet":             "Kindnet",
	"kube-multus-ds":      "Multus",
}

// cniImageSubstrings maps CNI container image fragments to the CNI
// implementation. This catches renamed DaemonSets.
var cniImageSubstrings = map[string]string{
	"calico/node":      "Calico",
	"flannel":          "Flannel",
	"cilium":           "Cilium",
	"ovn-kube":         "OVN-Kubernetes",
	"kube-router":      "Kube-router",
	"weaveworks/weave": "Weave Net",
	"antrea":           "Antrea",
	"amazon-k8s-cni":   "Amazon VPC CNI",
	"aws-vpc-cni":      "Amazon VPC CNI",
	"terway":           "Terway",
	"kindnet":          "Kindnet",
	"multus":           "Multus",
}

func (m *Network) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	seen := make(map[string]bool)

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range nodes.Items {
			node := &nodes.Items[i]
			for key := range node.Annotations {
				for prefix, cni := range cniNodeAnnotationPrefixes {
					if strings.HasPrefix(key, prefix) {
						seen[cni] = true
					}
				}
			}
		}
	}

	daemonSets, err := clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range daemonSets.Items {
			ds := &daemonSets.Items[i]
			if cni, ok := cniDaemonSetNames[strings.ToLower(ds.Name)]; ok {
				seen[cni] = true
			}
			for _, container := range ds.Spec.Template.Spec.Containers {
				detectCNIFromImage(container.Image, seen)
			}
			for _, container := range ds.Spec.Template.Spec.InitContainers {
				detectCNIFromImage(container.Image, seen)
			}
		}
	}

	// kubeadm clusters store the pod/service subnets and DNS domain in the
	// kubeadm-config ConfigMap. This is also how many managed kubeadm-based
	// distros expose them.
	kubeadmPod, kubeadmService, dnsDomain := kubeadmNetworkConfig(clientset, ctx)
	m.podCIDRs = append(m.podCIDRs, kubeadmPod...)
	m.serviceCIDRs = append(m.serviceCIDRs, kubeadmService...)
	m.dnsDomain = dnsDomain

	m.cni = make([]string, 0, len(seen))
	for cni := range seen {
		m.cni = append(m.cni, cni)
	}
	sort.Strings(m.cni)

	m.podCIDRs = uniqueSortedCIDRs(m.podCIDRs)
	m.serviceCIDRs = uniqueSortedCIDRs(m.serviceCIDRs)
	return nil
}

func detectCNIFromImage(image string, seen map[string]bool) {
	image = strings.ToLower(image)
	for fragment, cni := range cniImageSubstrings {
		if strings.Contains(image, fragment) {
			seen[cni] = true
		}
	}
}

type kubeadmClusterConfiguration struct {
	Networking struct {
		PodSubnet     string `yaml:"podSubnet"`
		ServiceSubnet string `yaml:"serviceSubnet"`
		DNSDomain     string `yaml:"dnsDomain"`
	} `yaml:"networking"`
}

func kubeadmNetworkConfig(clientset kubernetes.Interface, ctx context.Context) (podSubnets, serviceSubnets []string, dnsDomain string) {
	cm, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "kubeadm-config", metav1.GetOptions{})
	if err != nil {
		return nil, nil, ""
	}
	raw := cm.Data["ClusterConfiguration"]
	if raw == "" {
		return nil, nil, ""
	}

	var cfg kubeadmClusterConfiguration
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, nil, ""
	}
	for _, s := range strings.Split(cfg.Networking.PodSubnet, ",") {
		if s = strings.TrimSpace(s); s != "" {
			podSubnets = append(podSubnets, s)
		}
	}
	for _, s := range strings.Split(cfg.Networking.ServiceSubnet, ",") {
		if s = strings.TrimSpace(s); s != "" {
			serviceSubnets = append(serviceSubnets, s)
		}
	}
	return podSubnets, serviceSubnets, cfg.Networking.DNSDomain
}

func uniqueSortedCIDRs(cidrs []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func (m Network) Print(w io.Writer) error {
	var buf strings.Builder

	buf.WriteString("CNI:\n")
	if len(m.cni) == 0 {
		buf.WriteString("  (unknown)\n")
	} else {
		for _, cni := range m.cni {
			fmt.Fprintf(&buf, "  - %s\n", cni)
		}
	}

	buf.WriteString("\nDNS Domain:\n")
	if m.dnsDomain == "" {
		buf.WriteString("  (unknown)\n")
	} else {
		fmt.Fprintf(&buf, "  %s\n", m.dnsDomain)
	}

	buf.WriteString("\nPod CIDRs:\n")
	if len(m.podCIDRs) == 0 {
		buf.WriteString("  (unknown)\n")
	} else {
		for _, c := range m.podCIDRs {
			fmt.Fprintf(&buf, "  - %s\n", c)
		}
	}

	buf.WriteString("\nService CIDRs:\n")
	if len(m.serviceCIDRs) == 0 {
		buf.WriteString("  (unknown)\n")
	} else {
		for _, c := range m.serviceCIDRs {
			fmt.Fprintf(&buf, "  - %s\n", c)
		}
	}

	_, err := io.WriteString(w, buf.String())
	return err
}
