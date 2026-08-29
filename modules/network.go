package modules

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Network struct {
	ModuleAbstract

	cni []string
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
			for key := range nodes.Items[i].Annotations {
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

	m.cni = make([]string, 0, len(seen))
	for cni := range seen {
		m.cni = append(m.cni, cni)
	}
	sort.Strings(m.cni)
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

	_, err := io.WriteString(w, buf.String())
	return err
}
