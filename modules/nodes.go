package modules

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type Nodes struct {
	ModuleAbstract

	nodeCount          int
	unschedulableCount int
	notReadyCount      int
	masterCount        int
	kubeletVersions    map[string]int
	containerRuntime   map[string]int
	kernelVersions     map[string]int

	totalCPUMilli    int64
	totalMemoryBytes int64
	hugepages        map[string]resource.Quantity
	extended         map[string]resource.Quantity

	capacityCPUMilli    int64
	capacityMemoryBytes int64
	capacityHugepages   map[string]resource.Quantity
	capacityExtended    map[string]resource.Quantity

	wellKnownLabels map[string]map[string]int
}

func (m Nodes) Name() string {
	return "Nodes"
}

func (m Nodes) ID() string {
	return strings.ToLower(m.Name())
}

func (m Nodes) Visible() bool {
	return true
}

func (m Nodes) EnableByDefault() bool {
	return true
}

func (m *Nodes) Run() error {
	ctx := m.getContext()

	nodes, err := m.getCache().Nodes(ctx)
	if err != nil {
		return err
	}

	m.nodeCount = 0
	m.unschedulableCount = 0
	m.notReadyCount = 0
	m.masterCount = 0
	m.kubeletVersions = make(map[string]int)
	m.containerRuntime = make(map[string]int)
	m.kernelVersions = make(map[string]int)
	m.hugepages = make(map[string]resource.Quantity)
	m.extended = make(map[string]resource.Quantity)
	m.capacityHugepages = make(map[string]resource.Quantity)
	m.capacityExtended = make(map[string]resource.Quantity)
	m.wellKnownLabels = make(map[string]map[string]int, len(wellKnownNodeLabels))
	for _, key := range wellKnownNodeLabels {
		m.wellKnownLabels[key] = make(map[string]int)
	}

	for i := range nodes.Items {
		node := &nodes.Items[i]

		// Skip virtual-kubelet (e.g. VCI/CCI) nodes; they report synthetic
		// capacity and skew the totals.
		if node.Labels["type"] == "virtual-kubelet" {
			continue
		}
		m.nodeCount++
		if node.Spec.Unschedulable {
			m.unschedulableCount++
		}
		if !isNodeReady(node) {
			m.notReadyCount++
		}
		if isMasterNode(node) {
			m.masterCount++
		}

		info := node.Status.NodeInfo

		kubelet := kubeletMinor(info.KubeletVersion)
		if kubelet == "" {
			kubelet = "(unknown)"
		}
		m.kubeletVersions[kubelet]++

		m.containerRuntime[containerRuntimeName(info.ContainerRuntimeVersion)]++

		kernel := info.KernelVersion
		if kernel == "" {
			kernel = "(unknown)"
		}
		m.kernelVersions[kernel]++

		for _, key := range wellKnownNodeLabels {
			value, ok := node.Labels[key]
			if !ok || value == "" {
				value = "(unset)"
			}
			m.wellKnownLabels[key][value]++
		}

		for name, q := range node.Status.Allocatable {
			switch name {
			case corev1.ResourceCPU:
				m.totalCPUMilli += q.MilliValue()
			case corev1.ResourceMemory:
				m.totalMemoryBytes += q.Value()
			default:
				if strings.HasPrefix(string(name), corev1.ResourceHugePagesPrefix) {
					addQuantity(m.hugepages, string(name), q)
				} else if name != corev1.ResourcePods && name != corev1.ResourceEphemeralStorage {
					addQuantity(m.extended, string(name), q)
				}
			}
		}

		for name, q := range node.Status.Capacity {
			switch name {
			case corev1.ResourceCPU:
				m.capacityCPUMilli += q.MilliValue()
			case corev1.ResourceMemory:
				m.capacityMemoryBytes += q.Value()
			default:
				if strings.HasPrefix(string(name), corev1.ResourceHugePagesPrefix) {
					addQuantity(m.capacityHugepages, string(name), q)
				} else if name != corev1.ResourcePods && name != corev1.ResourceEphemeralStorage {
					addQuantity(m.capacityExtended, string(name), q)
				}
			}
		}
	}

	return nil
}

func (m Nodes) Print(w io.Writer) error {
	var buf strings.Builder

	writeNodeCountTable(&buf, m)
	writeCountTable(&buf, "Kubelet Versions", "Version", m.kubeletVersions)
	writeCountTable(&buf, "Container Runtimes", "Runtime", m.containerRuntime)
	writeCountTable(&buf, "Kernel Versions", "Kernel Version", m.kernelVersions)
	writeWellKnownLabelsTable(&buf, m.wellKnownLabels)
	writeResourcesTable(&buf, &m)

	_, err := io.WriteString(w, buf.String())
	return err
}

func kubeletMinor(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

func containerRuntimeName(v string) string {
	if v == "" {
		return "(unknown)"
	}
	if i := strings.Index(v, "://"); i >= 0 {
		return v[:i]
	}
	return v
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// isMasterNode reports whether the node has a control-plane/master role label.
// Kubernetes v1.20+ uses node-role.kubernetes.io/control-plane; older
// clusters use node-role.kubernetes.io/master.
func isMasterNode(node *corev1.Node) bool {
	for key := range node.Labels {
		if key == "node-role.kubernetes.io/control-plane" || key == "node-role.kubernetes.io/master" {
			return true
		}
	}
	return false
}

// wellKnownNodeLabels are the node classification labels defined in
// k8s.io/api/core/v1/well_known_labels.go. LabelHostname is excluded
// because it is unique per node, not a meaningful grouping dimension.
var wellKnownNodeLabels = []string{
	corev1.LabelArchStable,
	corev1.LabelOSStable,
	corev1.LabelTopologyRegion,
	corev1.LabelTopologyZone,
	corev1.LabelInstanceTypeStable,
	// corev1.LabelInstanceType,
	corev1.LabelWindowsBuild,
	// corev1.LabelFailureDomainBetaZone,
	// corev1.LabelFailureDomainBetaRegion,
}

func formatCPUCores(milli int64) string {
	return strconv.FormatFloat(float64(milli)/1000, 'f', -1, 64)
}

func formatBytes(b int64) string {
	const unit = 1024.0
	if b < 1024 {
		return fmt.Sprintf("%dB", b)
	}
	v := float64(b)
	for _, suffix := range []string{"Ki", "Mi", "Gi", "Ti", "Pi", "Ei"} {
		v /= unit
		if v < unit || suffix == "Ei" {
			return fmt.Sprintf("%.1f%s", v, suffix)
		}
	}
	return fmt.Sprintf("%dB", b)
}

func writeNodeCountTable(buf *strings.Builder, m Nodes) {
	fmt.Fprintf(buf, "Node Count:\n")
	rows := [][]string{
		{"Total", strconv.Itoa(m.nodeCount)},
		{"Unschedulable", strconv.Itoa(m.unschedulableCount)},
		{"Not Ready", strconv.Itoa(m.notReadyCount)},
		{"Master", strconv.Itoa(m.masterCount)},
	}
	renderTable(buf, []string{"Metric", "Count"}, rows)
}

func writeCountTable(buf *strings.Builder, title, itemHeader string, counts map[string]int) {
	fmt.Fprintf(buf, "%s:\n", title)
	if len(counts) == 0 {
		buf.WriteString("(none)\n")
		return
	}
	rows := make([][]string, 0, len(counts))
	for _, item := range sortedIntKeys(counts) {
		rows = append(rows, []string{item, strconv.Itoa(counts[item])})
	}
	renderTable(buf, []string{itemHeader, "Count"}, rows)
}

func writeWellKnownLabelsTable(buf *strings.Builder, data map[string]map[string]int) {
	fmt.Fprintf(buf, "Well-Known Labels:\n")
	for _, key := range wellKnownNodeLabels {
		counts := data[key]
		rows := make([][]string, 0, len(counts))
		hasValue := false
		for _, value := range sortedIntKeys(counts) {
			if counts[value] == 0 {
				continue
			}
			if value != "(unset)" {
				hasValue = true
			}
			rows = append(rows, []string{value, strconv.Itoa(counts[value])})
		}
		if !hasValue {
			continue
		}
		renderTable(buf, []string{key, "Count"}, rows)
	}
}

func writeResourcesTable(buf *strings.Builder, m *Nodes) {
	fmt.Fprintf(buf, "Resources:\n")
	rows := [][]string{
		{"CPU", formatCPUCores(m.capacityCPUMilli), formatCPUCores(m.totalCPUMilli)},
		{"Memory", formatBytes(m.capacityMemoryBytes), formatBytes(m.totalMemoryBytes)},
	}
	for _, name := range resourceNames(m.capacityHugepages, m.hugepages, m.capacityExtended, m.extended) {
		capacity := lookupQuantity(name, m.capacityHugepages, m.capacityExtended)
		allocatable := lookupQuantity(name, m.hugepages, m.extended)
		rows = append(rows, []string{name, capacity, allocatable})
	}
	renderTable(buf, []string{"Resource", "Capacity", "Allocatable"}, rows)
}

func renderTable(buf *strings.Builder, headers []string, rows [][]string) {
	table := tablewriter.NewTable(buf,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Symbols: tw.NewSymbols(tw.StyleLight),
		})),
		tablewriter.WithHeaderAutoFormat(tw.Off),
	)
	anyHeaders := make([]any, len(headers))
	for i, h := range headers {
		anyHeaders[i] = h
	}
	table.Header(anyHeaders...)
	for _, row := range rows {
		_ = table.Append(row)
	}
	_ = table.Render()
}

func resourceNames(maps ...map[string]resource.Quantity) []string {
	seen := make(map[string]bool)
	for _, m := range maps {
		for name := range m {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func lookupQuantity(name string, hugepages, extended map[string]resource.Quantity) string {
	var m map[string]resource.Quantity
	if strings.HasPrefix(name, corev1.ResourceHugePagesPrefix) {
		m = hugepages
	} else {
		m = extended
	}
	if q, ok := m[name]; ok {
		return q.String()
	}
	return "-"
}

func addQuantity(m map[string]resource.Quantity, name string, q resource.Quantity) {
	if cur, ok := m[name]; ok {
		cur.Add(q)
		m[name] = cur
		return
	}
	m[name] = q
}

func sortedIntKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
