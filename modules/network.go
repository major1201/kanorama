package modules

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Network struct {
	ModuleAbstract

	cni          []string
	podCIDRs     []string
	serviceCIDRs []string
	dnsDomain    string
	dnsServices  []dnsServiceInfo
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

	// Trace the cluster DNS provider from the actual 53/UDP+TCP services and
	// their backing pods, rather than guessing from well-known names.
	var pods *corev1.PodList
	if cachedPods, err := m.getCache().Pods(ctx); err == nil {
		pods = cachedPods
	}
	m.dnsServices = detectDNSServices(clientset, ctx, pods)

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

type dnsServiceInfo struct {
	namespace string
	name      string
	clusterIP string
	ports     string
	provider  string
	endpoints int
}

// detectDNSServices finds every Service exposing port 53 (UDP or TCP) and
// traces its endpoints back to the backing pods to determine who actually
// serves cluster DNS.
func detectDNSServices(clientset kubernetes.Interface, ctx context.Context, pods *corev1.PodList) []dnsServiceInfo {
	svcList, err := clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}

	podByRef := make(map[string]*corev1.Pod)
	if pods != nil {
		for i := range pods.Items {
			pod := &pods.Items[i]
			podByRef[pod.Namespace+"/"+pod.Name] = pod
		}
	}

	var out []dnsServiceInfo
	for i := range svcList.Items {
		svc := &svcList.Items[i]

		ports := make([]string, 0, len(svc.Spec.Ports))
		for _, p := range svc.Spec.Ports {
			if p.Port != 53 {
				continue
			}
			proto := string(p.Protocol)
			if proto == "" {
				proto = "TCP"
			}
			ports = append(ports, fmt.Sprintf("53/%s", proto))
		}
		if len(ports) == 0 {
			continue
		}
		sort.Strings(ports)

		info := dnsServiceInfo{
			namespace: svc.Namespace,
			name:      svc.Name,
			clusterIP: svc.Spec.ClusterIP,
			ports:     strings.Join(ports, ","),
		}

		eps, err := clientset.CoreV1().Endpoints(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
		if err == nil {
			providers := make(map[string]bool)
			ips := make(map[string]bool)
			for _, subset := range eps.Subsets {
				for _, addr := range subset.Addresses {
					ips[addr.IP] = true
					if addr.TargetRef != nil && addr.TargetRef.Kind == "Pod" {
						if pod := podByRef[addr.TargetRef.Namespace+"/"+addr.TargetRef.Name]; pod != nil {
							if provider := dnsProvider(pod); provider != "" {
								providers[provider] = true
							}
						}
					}
				}
			}
			info.endpoints = len(ips)
			info.provider = strings.Join(sortedStringSet(providers), ",")
		}

		out = append(out, info)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].namespace != out[j].namespace {
			return out[i].namespace < out[j].namespace
		}
		return out[i].name < out[j].name
	})
	return out
}

// dnsProvider identifies the DNS software backing a pod from its container
// image and command line, without depending on service or deployment names.
func dnsProvider(pod *corev1.Pod) string {
	containers := make([]corev1.Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	containers = append(containers, pod.Spec.Containers...)
	containers = append(containers, pod.Spec.InitContainers...)
	for i := range containers {
		c := &containers[i]

		img := strings.ToLower(c.Image)
		switch {
		case strings.Contains(img, "coredns"):
			return "CoreDNS"
		case strings.Contains(img, "kube-dns") || strings.Contains(img, "k8s-dns"):
			return "kube-dns"
		case strings.Contains(img, "node-local-dns") || strings.Contains(img, "node-cache"):
			return "NodeLocal DNSCache"
		}

		for _, arg := range c.Command {
			if strings.Contains(strings.ToLower(arg), "coredns") {
				return "CoreDNS"
			}
			if strings.Contains(strings.ToLower(arg), "kube-dns") {
				return "kube-dns"
			}
		}
		for _, arg := range c.Args {
			if strings.Contains(strings.ToLower(arg), "coredns") {
				return "CoreDNS"
			}
			if strings.Contains(strings.ToLower(arg), "kube-dns") {
				return "kube-dns"
			}
		}
	}
	return ""
}

func sortedStringSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
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

	buf.WriteString("\nCluster DNS:\n")
	if len(m.dnsServices) == 0 {
		buf.WriteString("  (no service exposing port 53 found)\n")
	} else {
		rows := make([][]string, 0, len(m.dnsServices))
		for _, s := range m.dnsServices {
			provider := s.provider
			if provider == "" {
				provider = "(unknown)"
			}
			rows = append(rows, []string{s.namespace, s.name, s.clusterIP, s.ports, provider, strconv.Itoa(s.endpoints)})
		}
		renderTable(&buf, []string{"Namespace", "Service", "ClusterIP", "Ports", "Provider", "Endpoints"}, rows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}
