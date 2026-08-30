package modules

import (
	"io"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DaemonSet struct {
	ModuleAbstract

	rows [][]string
}

func (m DaemonSet) Name() string {
	return "DaemonSet"
}

func (m DaemonSet) ID() string {
	return strings.ToLower(m.Name())
}

func (m DaemonSet) Visible() bool {
	return true
}

func (m DaemonSet) EnableByDefault() bool {
	return true
}

func (m *DaemonSet) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	list, err := clientset.AppsV1().DaemonSets("").List(m.getContext(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	sort.Slice(list.Items, func(i, j int) bool {
		if list.Items[i].Namespace != list.Items[j].Namespace {
			return list.Items[i].Namespace < list.Items[j].Namespace
		}
		return list.Items[i].Name < list.Items[j].Name
	})

	m.rows = make([][]string, 0, len(list.Items))
	for i := range list.Items {
		ds := &list.Items[i]
		m.rows = append(m.rows, []string{
			ds.Namespace,
			ds.Name,
			strconv.Itoa(int(ds.Status.DesiredNumberScheduled)),
			strconv.Itoa(int(ds.Status.CurrentNumberScheduled)),
			strconv.Itoa(int(ds.Status.NumberReady)),
			formatNodeSelector(ds.Spec.Template.Spec.NodeSelector),
			daemonsetResources(ds),
		})
	}

	return nil
}

func (m DaemonSet) Print(w io.Writer) error {
	var buf strings.Builder

	buf.WriteString("DaemonSets:\n")
	if len(m.rows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Namespace", "Name", "Desired", "Current", "Ready", "NodeSelector", "Resources"}, m.rows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

func formatNodeSelector(selector map[string]string) string {
	if len(selector) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+selector[key])
	}
	return strings.Join(parts, ",")
}

func daemonsetResources(ds *appsv1.DaemonSet) string {
	req := make(map[corev1.ResourceName]resource.Quantity)
	lim := make(map[corev1.ResourceName]resource.Quantity)

	containers := make([]corev1.Container, 0, len(ds.Spec.Template.Spec.Containers)+len(ds.Spec.Template.Spec.InitContainers))
	containers = append(containers, ds.Spec.Template.Spec.Containers...)
	containers = append(containers, ds.Spec.Template.Spec.InitContainers...)
	for i := range containers {
		for name, q := range containers[i].Resources.Requests {
			addResourceNameQuantity(req, name, q)
		}
		for name, q := range containers[i].Resources.Limits {
			addResourceNameQuantity(lim, name, q)
		}
	}

	reqStr := formatResourceMap(req)
	limStr := formatResourceMap(lim)
	if reqStr == "" && limStr == "" {
		return "-"
	}
	var parts []string
	if reqStr != "" {
		parts = append(parts, "req("+reqStr+")")
	}
	if limStr != "" {
		parts = append(parts, "lim("+limStr+")")
	}
	return strings.Join(parts, " ")
}

func formatResourceMap(m map[corev1.ResourceName]resource.Quantity) string {
	if len(m) == 0 {
		return ""
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, string(name))
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		q := m[corev1.ResourceName(n)]
		parts = append(parts, n+"="+q.String())
	}
	return strings.Join(parts, ",")
}
