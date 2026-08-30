package modules

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type Pods struct {
	ModuleAbstract

	statusCounts map[string]int
	total        int

	resourcePods  int
	requestTotals map[corev1.ResourceName]resource.Quantity
	limitTotals   map[corev1.ResourceName]resource.Quantity

	nodeErr        error
	nodeCPUAlloc   int64
	nodeMemAlloc   int64
	nodeOtherAlloc map[corev1.ResourceName]resource.Quantity

	kindAggs map[string]*resAgg
	nsAggs   map[string]*resAgg
}

type resAgg struct {
	key         string
	pods        int
	cpuReqMilli int64
	cpuLimMilli int64
	memReqBytes int64
	memLimBytes int64
	ext         map[corev1.ResourceName]resource.Quantity
}

func (m Pods) Name() string {
	return "Pods"
}

func (m Pods) ID() string {
	return strings.ToLower(m.Name())
}

func (m Pods) Visible() bool {
	return true
}

func (m Pods) EnableByDefault() bool {
	return true
}

func (m *Pods) Run() error {
	ctx := m.getContext()
	cache := m.getCache()

	pods, err := cache.Pods(ctx)
	if err != nil {
		return err
	}

	clientset, err := newClientset()
	if err != nil {
		return err
	}
	owners := buildOwnerIndex(ctx, clientset)

	nodes, err := cache.Nodes(ctx)
	if err != nil {
		m.nodeErr = err
	} else {
		m.nodeCPUAlloc, m.nodeMemAlloc, m.nodeOtherAlloc = nodeAllocatableTotals(nodes)
	}

	m.statusCounts = make(map[string]int)
	m.requestTotals = make(map[corev1.ResourceName]resource.Quantity)
	m.limitTotals = make(map[corev1.ResourceName]resource.Quantity)
	m.kindAggs = make(map[string]*resAgg)
	m.nsAggs = make(map[string]*resAgg)

	for i := range pods.Items {
		pod := &pods.Items[i]

		m.total++
		status := podStatusCategory(pod)
		m.statusCounts[status]++

		topKind := topLevelKind(pod, owners)
		agg := m.kindAggs[topKind]
		if agg == nil {
			agg = &resAgg{key: topKind}
			m.kindAggs[topKind] = agg
		}
		nsAgg := m.nsAggs[pod.Namespace]
		if nsAgg == nil {
			nsAgg = &resAgg{key: pod.Namespace}
			m.nsAggs[pod.Namespace] = nsAgg
		}
		agg.pods++
		nsAgg.pods++

		if !isResourceConsuming(pod) {
			continue
		}
		m.resourcePods++

		req := make(map[corev1.ResourceName]resource.Quantity)
		lim := make(map[corev1.ResourceName]resource.Quantity)
		sumPodResources(pod, req, lim)

		for name, q := range req {
			addResourceNameQuantity(m.requestTotals, name, q)
		}
		for name, q := range lim {
			addResourceNameQuantity(m.limitTotals, name, q)
		}

		cpuReq := req[corev1.ResourceCPU]
		cpuLim := lim[corev1.ResourceCPU]
		memReq := req[corev1.ResourceMemory]
		memLim := lim[corev1.ResourceMemory]
		agg.cpuReqMilli += cpuReq.MilliValue()
		agg.cpuLimMilli += cpuLim.MilliValue()
		agg.memReqBytes += memReq.Value()
		agg.memLimBytes += memLim.Value()
		nsAgg.cpuReqMilli += cpuReq.MilliValue()
		nsAgg.cpuLimMilli += cpuLim.MilliValue()
		nsAgg.memReqBytes += memReq.Value()
		nsAgg.memLimBytes += memLim.Value()

		for name, q := range req {
			if isExtendedResource(name) {
				if agg.ext == nil {
					agg.ext = make(map[corev1.ResourceName]resource.Quantity)
				}
				addResourceNameQuantity(agg.ext, name, q)
				if nsAgg.ext == nil {
					nsAgg.ext = make(map[corev1.ResourceName]resource.Quantity)
				}
				addResourceNameQuantity(nsAgg.ext, name, q)
			}
		}
	}

	return nil
}

func (m Pods) Print(w io.Writer) error {
	var buf strings.Builder

	writePodStatusTable(&buf, m)
	writePodResourcesTable(&buf, &m)
	writeKindTopTables(&buf, m)

	_, err := io.WriteString(w, buf.String())
	return err
}

func writePodStatusTable(buf *strings.Builder, m Pods) {
	buf.WriteString("Pod Status:\n")
	rows := [][]string{
		{"Total", strconv.Itoa(m.total)},
	}
	for _, status := range []string{"Running", "Pending", "Init", "Terminating", "Succeeded", "Failed", "Unknown"} {
		rows = append(rows, []string{status, strconv.Itoa(m.statusCounts[status])})
	}
	renderTable(buf, []string{"Status", "Count"}, rows)
}

func writePodResourcesTable(buf *strings.Builder, m *Pods) {
	buf.WriteString("Resource Usage (non-Pending, non-Terminating):\n")
	if m.nodeErr != nil {
		fmt.Fprintf(buf, "  (node totals unavailable: %v)\n", m.nodeErr)
	}

	rows := make([][]string, 0)
	for _, name := range podResourceNames(m.requestTotals, m.limitTotals, m.nodeOtherAlloc) {
		rows = append(rows, []string{
			resourceDisplayName(name),
			formatNodeAllocatable(name, m.nodeCPUAlloc, m.nodeMemAlloc, m.nodeOtherAlloc),
			formatPodResource(name, m.requestTotals),
			formatPodResource(name, m.limitTotals),
			podResourcePercent(name, m.requestTotals, m.nodeCPUAlloc, m.nodeMemAlloc, m.nodeOtherAlloc),
			podResourcePercent(name, m.limitTotals, m.nodeCPUAlloc, m.nodeMemAlloc, m.nodeOtherAlloc),
		})
	}
	renderTable(buf, []string{"Resource", "Allocatable", "Request", "Limit", "Request %", "Limit %"}, rows)
}

func writeKindTopTables(buf *strings.Builder, m Pods) {
	writeTopPair(buf, m.kindAggs, m.nsAggs,
		"Top 5 Kinds by Pod Count", "Top 5 Namespaces by Pod Count",
		"Pods",
		func(a *resAgg) int64 { return int64(a.pods) },
		func(a *resAgg) string { return strconv.Itoa(a.pods) },
		int64(m.total))

	buf.WriteString(mergeTables(
		topTableString("Top 5 Kinds by CPU Request", "Kind", "CPU Request",
			topAggRows(m.kindAggs,
				func(a *resAgg) int64 { return a.cpuReqMilli },
				func(a *resAgg) string { return formatCPUCores(a.cpuReqMilli) },
				m.nodeCPUAlloc)),
		topTableString("Top 5 Kinds by CPU Limit", "Kind", "CPU Limit",
			topAggRows(m.kindAggs,
				func(a *resAgg) int64 { return a.cpuLimMilli },
				func(a *resAgg) string { return formatCPUCores(a.cpuLimMilli) },
				m.nodeCPUAlloc)),
		topTableString("Top 5 Namespaces by CPU Request", "Namespace", "CPU Request",
			topAggRows(m.nsAggs,
				func(a *resAgg) int64 { return a.cpuReqMilli },
				func(a *resAgg) string { return formatCPUCores(a.cpuReqMilli) },
				m.nodeCPUAlloc)),
		topTableString("Top 5 Namespaces by CPU Limit", "Namespace", "CPU Limit",
			topAggRows(m.nsAggs,
				func(a *resAgg) int64 { return a.cpuLimMilli },
				func(a *resAgg) string { return formatCPUCores(a.cpuLimMilli) },
				m.nodeCPUAlloc)),
	))

	buf.WriteString(mergeTables(
		topTableString("Top 5 Kinds by Memory Request", "Kind", "Memory Request",
			topAggRows(m.kindAggs,
				func(a *resAgg) int64 { return a.memReqBytes },
				func(a *resAgg) string { return formatBytes(a.memReqBytes) },
				m.nodeMemAlloc)),
		topTableString("Top 5 Kinds by Memory Limit", "Kind", "Memory Limit",
			topAggRows(m.kindAggs,
				func(a *resAgg) int64 { return a.memLimBytes },
				func(a *resAgg) string { return formatBytes(a.memLimBytes) },
				m.nodeMemAlloc)),
		topTableString("Top 5 Namespaces by Memory Request", "Namespace", "Memory Request",
			topAggRows(m.nsAggs,
				func(a *resAgg) int64 { return a.memReqBytes },
				func(a *resAgg) string { return formatBytes(a.memReqBytes) },
				m.nodeMemAlloc)),
		topTableString("Top 5 Namespaces by Memory Limit", "Namespace", "Memory Limit",
			topAggRows(m.nsAggs,
				func(a *resAgg) int64 { return a.memLimBytes },
				func(a *resAgg) string { return formatBytes(a.memLimBytes) },
				m.nodeMemAlloc)),
	))

	writeExtendedKindTopTables(buf, m)
}

func writeExtendedKindTopTables(buf *strings.Builder, m Pods) {
	names := make([]corev1.ResourceName, 0)
	for name := range m.requestTotals {
		if isExtendedResource(name) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	for _, name := range names {
		var total int64
		if m.nodeOtherAlloc != nil {
			if q, ok := m.nodeOtherAlloc[name]; ok {
				total = q.Value()
			}
		}
		writeTopPair(buf, m.kindAggs, m.nsAggs,
			"Top 5 Kinds by "+string(name), "Top 5 Namespaces by "+string(name),
			string(name),
			func(a *resAgg) int64 { return aggExtValue(a, name) },
			func(a *resAgg) string {
				if a.ext == nil {
					return "-"
				}
				q := a.ext[name]
				if q.IsZero() {
					return "0"
				}
				return q.String()
			},
			total)
	}
}

func writeTopPair(buf *strings.Builder, kinds, nss map[string]*resAgg, kindTitle, nsTitle, valueHeader string, metric func(*resAgg) int64, format func(*resAgg) string, total int64) {
	left := topTableString(kindTitle, "Kind", valueHeader, topAggRows(kinds, metric, format, total))
	right := topTableString(nsTitle, "Namespace", valueHeader, topAggRows(nss, metric, format, total))
	buf.WriteString(mergeSideBySide(left, right))
}

func isExtendedResource(name corev1.ResourceName) bool {
	switch name {
	case corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods, corev1.ResourceEphemeralStorage:
		return false
	default:
		return true
	}
}

func aggExtValue(a *resAgg, name corev1.ResourceName) int64 {
	if a.ext == nil {
		return 0
	}
	q := a.ext[name]
	return q.Value()
}

func topAggRows(aggs map[string]*resAgg, metric func(*resAgg) int64, format func(*resAgg) string, total int64) [][]string {
	list := sortedAggs(aggs, metric)
	rows := make([][]string, 0, 5)
	for _, a := range list {
		if metric(a) == 0 {
			continue
		}
		rows = append(rows, []string{a.key, format(a), percentString(metric(a), total)})
		if len(rows) == 5 {
			break
		}
	}
	return rows
}

func percentString(value, total int64) string {
	if total <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(value)*100/float64(total))
}

func topTableString(title, keyHeader, valueHeader string, rows [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", title)
	if len(rows) == 0 {
		b.WriteString("(none)\n")
		return b.String()
	}
	renderTable(&b, []string{keyHeader, valueHeader, "%"}, rows)
	return b.String()
}

func mergeSideBySide(left, right string) string {
	return mergeTables(left, right)
}

func mergeTables(tables ...string) string {
	lines := make([][]string, len(tables))
	widths := make([]int, len(tables))
	rows := 0
	for i, t := range tables {
		ls := strings.Split(strings.TrimRight(t, "\n"), "\n")
		lines[i] = ls
		if len(ls) > rows {
			rows = len(ls)
		}
		for _, line := range ls {
			if w := runewidth.StringWidth(line); w > widths[i] {
				widths[i] = w
			}
		}
	}

	var b strings.Builder
	for r := 0; r < rows; r++ {
		last := -1
		for i := range tables {
			if r < len(lines[i]) && lines[i][r] != "" {
				last = i
			}
		}
		for i := 0; i <= last; i++ {
			var cell string
			if r < len(lines[i]) {
				cell = lines[i][r]
			}
			if i > 0 {
				b.WriteString("    ")
			}
			b.WriteString(padRight(cell, widths[i]))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func padRight(s string, width int) string {
	if w := runewidth.StringWidth(s); w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-runewidth.StringWidth(s))
}

func sortedAggs(aggs map[string]*resAgg, metric func(*resAgg) int64) []*resAgg {
	list := make([]*resAgg, 0, len(aggs))
	for _, a := range aggs {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if metric(list[i]) != metric(list[j]) {
			return metric(list[i]) > metric(list[j])
		}
		return list[i].key < list[j].key
	})
	return list
}

func podResourceNames(req, lim, nodeOther map[corev1.ResourceName]resource.Quantity) []corev1.ResourceName {
	seen := map[corev1.ResourceName]bool{corev1.ResourceCPU: true, corev1.ResourceMemory: true}
	for name := range req {
		seen[name] = true
	}
	for name := range lim {
		seen[name] = true
	}
	for name := range nodeOther {
		seen[name] = true
	}

	names := make([]corev1.ResourceName, 0, len(seen))
	for name := range seen {
		if name == corev1.ResourceCPU || name == corev1.ResourceMemory || name == corev1.ResourcePods || name == corev1.ResourceEphemeralStorage {
			continue
		}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	out := []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}
	return append(out, names...)
}

func resourceDisplayName(name corev1.ResourceName) string {
	switch name {
	case corev1.ResourceCPU:
		return "CPU"
	case corev1.ResourceMemory:
		return "Memory"
	default:
		return string(name)
	}
}

func formatPodResource(name corev1.ResourceName, totals map[corev1.ResourceName]resource.Quantity) string {
	q, ok := totals[name]
	if !ok {
		return "-"
	}
	switch name {
	case corev1.ResourceCPU:
		return formatCPUCores(q.MilliValue())
	case corev1.ResourceMemory:
		return formatBytes(q.Value())
	default:
		return q.String()
	}
}

func formatNodeAllocatable(name corev1.ResourceName, cpuAlloc, memAlloc int64, otherAlloc map[corev1.ResourceName]resource.Quantity) string {
	switch name {
	case corev1.ResourceCPU:
		return formatCPUCores(cpuAlloc)
	case corev1.ResourceMemory:
		return formatBytes(memAlloc)
	default:
		if q, ok := otherAlloc[name]; ok {
			return q.String()
		}
		return "-"
	}
}

func podResourcePercent(name corev1.ResourceName, totals map[corev1.ResourceName]resource.Quantity, cpuAlloc, memAlloc int64, otherAlloc map[corev1.ResourceName]resource.Quantity) string {
	q, ok := totals[name]
	if !ok {
		return "-"
	}
	var num, den int64
	switch name {
	case corev1.ResourceCPU:
		num, den = q.MilliValue(), cpuAlloc
	case corev1.ResourceMemory:
		num, den = q.Value(), memAlloc
	default:
		other := otherAlloc[name]
		den = other.MilliValue()
		num = q.MilliValue()
	}
	if den == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(num)*100/float64(den))
}

func podStatusCategory(pod *corev1.Pod) string {
	if pod.DeletionTimestamp != nil {
		return "Terminating"
	}
	if pod.Status.Phase == corev1.PodPending && hasIncompleteInitContainers(pod) {
		return "Init"
	}
	return string(pod.Status.Phase)
}

func hasIncompleteInitContainers(pod *corev1.Pod) bool {
	if len(pod.Spec.InitContainers) == 0 {
		return false
	}
	for i := range pod.Status.InitContainerStatuses {
		st := &pod.Status.InitContainerStatuses[i]
		if st.State.Terminated == nil || st.State.Terminated.ExitCode != 0 {
			return true
		}
	}
	return len(pod.Status.InitContainerStatuses) < len(pod.Spec.InitContainers)
}

func isResourceConsuming(pod *corev1.Pod) bool {
	return pod.DeletionTimestamp == nil && pod.Status.Phase != corev1.PodPending
}

func sumPodResources(pod *corev1.Pod, req, lim map[corev1.ResourceName]resource.Quantity) {
	containers := make([]corev1.Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	containers = append(containers, pod.Spec.Containers...)
	containers = append(containers, pod.Spec.InitContainers...)
	for i := range containers {
		for name, q := range containers[i].Resources.Requests {
			addResourceNameQuantity(req, name, q)
		}
		for name, q := range containers[i].Resources.Limits {
			addResourceNameQuantity(lim, name, q)
		}
	}
}

func addResourceNameQuantity(m map[corev1.ResourceName]resource.Quantity, name corev1.ResourceName, q resource.Quantity) {
	if cur, ok := m[name]; ok {
		cur.Add(q)
		m[name] = cur
		return
	}
	m[name] = q
}

func nodeAllocatableTotals(nodes *corev1.NodeList) (cpuMilli int64, memBytes int64, other map[corev1.ResourceName]resource.Quantity) {
	other = make(map[corev1.ResourceName]resource.Quantity)
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if node.Labels["type"] == "virtual-kubelet" {
			continue
		}
		for name, q := range node.Status.Allocatable {
			switch name {
			case corev1.ResourceCPU:
				cpuMilli += q.MilliValue()
			case corev1.ResourceMemory:
				memBytes += q.Value()
			default:
				if name != corev1.ResourcePods && name != corev1.ResourceEphemeralStorage {
					addResourceNameQuantity(other, name, q)
				}
			}
		}
	}
	return cpuMilli, memBytes, other
}

type ownerInfo struct {
	kind string
	refs []metav1.OwnerReference
}

func buildOwnerIndex(ctx context.Context, clientset *kubernetes.Clientset) map[types.UID]ownerInfo {
	owners := make(map[types.UID]ownerInfo)
	add := func(kind string, uid types.UID, refs []metav1.OwnerReference) {
		owners[uid] = ownerInfo{kind: kind, refs: refs}
	}

	if list, err := clientset.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			add("ReplicaSet", list.Items[i].UID, list.Items[i].OwnerReferences)
		}
	}
	if list, err := clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			add("Deployment", list.Items[i].UID, list.Items[i].OwnerReferences)
		}
	}
	if list, err := clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			add("StatefulSet", list.Items[i].UID, list.Items[i].OwnerReferences)
		}
	}
	if list, err := clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			add("DaemonSet", list.Items[i].UID, list.Items[i].OwnerReferences)
		}
	}
	if list, err := clientset.BatchV1().Jobs("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			add("Job", list.Items[i].UID, list.Items[i].OwnerReferences)
		}
	}
	if list, err := clientset.CoreV1().ReplicationControllers("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			add("ReplicationController", list.Items[i].UID, list.Items[i].OwnerReferences)
		}
	}
	return owners
}

func topLevelKind(pod *corev1.Pod, owners map[types.UID]ownerInfo) string {
	return resolveTopKind(pod.OwnerReferences, owners, "Pod")
}

func resolveTopKind(refs []metav1.OwnerReference, owners map[types.UID]ownerInfo, fallback string) string {
	ref := controllerOwner(refs)
	if ref == nil {
		return fallback
	}

	kind := ref.Kind
	uid := ref.UID
	for i := 0; i < 10; i++ {
		info, ok := owners[uid]
		if !ok {
			return kind
		}
		if next := controllerOwner(info.refs); next == nil {
			return info.kind
		} else {
			kind = next.Kind
			uid = next.UID
		}
	}
	return kind
}

func controllerOwner(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	if len(refs) > 0 {
		return &refs[0]
	}
	return nil
}
