package modules

import (
	"fmt"
	"io"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Kubelet struct {
	ModuleAbstract

	configYAML string
	source     string
}

func (m Kubelet) Name() string {
	return "Kubelet"
}

func (m Kubelet) ID() string {
	return strings.ToLower(m.Name())
}

func (m Kubelet) Visible() bool {
	return true
}

func (m Kubelet) EnableByDefault() bool {
	return true
}

func (m *Kubelet) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	cmList, err := clientset.CoreV1().ConfigMaps("kube-system").List(m.getContext(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	// kubeadm stores the KubeletConfiguration in a ConfigMap named either
	// "kubelet-config" (managed distros) or "kubelet-config-<version>"
	// (stock kubeadm). Prefer the unversioned name, otherwise the newest
	// versioned one.
	var candidates []string
	for i := range cmList.Items {
		name := cmList.Items[i].Name
		if name == "kubelet-config" {
			candidates = []string{name}
			break
		}
		if strings.HasPrefix(name, "kubelet-config") && cmList.Items[i].Data["kubelet"] != "" {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Strings(candidates)
	source := candidates[len(candidates)-1]

	for i := range cmList.Items {
		if cmList.Items[i].Name == source {
			m.source = source
			m.configYAML = cmList.Items[i].Data["kubelet"]
			break
		}
	}
	return nil
}

func (m Kubelet) Print(w io.Writer) error {
	var buf strings.Builder

	buf.WriteString("Kubelet Configuration:\n")
	if m.source == "" {
		buf.WriteString("(not found)\n")
	} else {
		fmt.Fprintf(&buf, "Source: kube-system/%s\n\n", m.source)
		buf.WriteString(m.configYAML)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}
