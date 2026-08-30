package modules

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Namespaces struct {
	ModuleAbstract

	total       int
	active      int
	terminating int

	rows      [][]string
	quotaRows [][]string
}

func (m Namespaces) Name() string {
	return "Namespaces"
}

func (m Namespaces) ID() string {
	return strings.ToLower(m.Name())
}

func (m Namespaces) Visible() bool {
	return true
}

func (m Namespaces) EnableByDefault() bool {
	return true
}

func (m *Namespaces) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}
	ctx := m.getContext()

	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	pods, podsErr := m.getCache().Pods(ctx)
	podCount := make(map[string]int)
	nsReq := make(map[string]map[corev1.ResourceName]resource.Quantity)
	nsLim := make(map[string]map[corev1.ResourceName]resource.Quantity)
	if podsErr == nil {
		for i := range pods.Items {
			pod := &pods.Items[i]
			podCount[pod.Namespace]++
			if nsReq[pod.Namespace] == nil {
				nsReq[pod.Namespace] = make(map[corev1.ResourceName]resource.Quantity)
			}
			if nsLim[pod.Namespace] == nil {
				nsLim[pod.Namespace] = make(map[corev1.ResourceName]resource.Quantity)
			}
			sumPodResources(pod, nsReq[pod.Namespace], nsLim[pod.Namespace])
		}
	}

	quotaCount := make(map[string]int)
	limitRangeCount := make(map[string]int)

	quotaList, err := clientset.CoreV1().ResourceQuotas("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range quotaList.Items {
			quotaCount[quotaList.Items[i].Namespace]++
		}
	}

	limitRangeList, err := clientset.CoreV1().LimitRanges("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range limitRangeList.Items {
			limitRangeCount[limitRangeList.Items[i].Namespace]++
		}
	}

	m.total = len(nsList.Items)
	m.rows = make([][]string, 0, m.total)
	for i := range nsList.Items {
		ns := &nsList.Items[i]
		status := string(ns.Status.Phase)
		switch ns.Status.Phase {
		case corev1.NamespaceActive:
			m.active++
		case corev1.NamespaceTerminating:
			m.terminating++
		}

		podsStr := "-"
		cpuStr := "-"
		memStr := "-"
		if podsErr == nil {
			podsStr = strconv.Itoa(podCount[ns.Name])
			cpuStr = formatCPUReqLim(nsReq[ns.Name][corev1.ResourceCPU], nsLim[ns.Name][corev1.ResourceCPU])
			memStr = formatMemoryReqLim(nsReq[ns.Name][corev1.ResourceMemory], nsLim[ns.Name][corev1.ResourceMemory])
		}

		m.rows = append(m.rows, []string{
			ns.Name,
			status,
			podsStr,
			cpuStr,
			memStr,
			strconv.Itoa(quotaCount[ns.Name]),
			strconv.Itoa(limitRangeCount[ns.Name]),
			ns.CreationTimestamp.Format("2006-01-02 15:04:05 -0700"),
		})
	}
	sort.Slice(m.rows, func(i, j int) bool { return m.rows[i][0] < m.rows[j][0] })

	if quotaList != nil {
		m.buildQuotaRows(quotaList)
	}

	return nil
}

func (m *Namespaces) buildQuotaRows(quotaList *corev1.ResourceQuotaList) {
	m.quotaRows = make([][]string, 0)
	for i := range quotaList.Items {
		q := &quotaList.Items[i]

		names := make(map[string]bool)
		for name := range q.Spec.Hard {
			names[string(name)] = true
		}
		for name := range q.Status.Used {
			names[string(name)] = true
		}
		keys := make([]string, 0, len(names))
		for name := range names {
			keys = append(keys, name)
		}
		sort.Strings(keys)

		for _, name := range keys {
			hard := q.Spec.Hard[corev1.ResourceName(name)]
			used := q.Status.Used[corev1.ResourceName(name)]
			m.quotaRows = append(m.quotaRows, []string{
				q.Namespace,
				q.Name,
				name,
				formatQuantity(hard),
				formatQuantity(used),
			})
		}
	}
	sort.Slice(m.quotaRows, func(i, j int) bool {
		if m.quotaRows[i][0] != m.quotaRows[j][0] {
			return m.quotaRows[i][0] < m.quotaRows[j][0]
		}
		if m.quotaRows[i][1] != m.quotaRows[j][1] {
			return m.quotaRows[i][1] < m.quotaRows[j][1]
		}
		return m.quotaRows[i][2] < m.quotaRows[j][2]
	})
}

func (m Namespaces) Print(w io.Writer) error {
	var buf strings.Builder

	fmt.Fprintf(&buf, "Namespaces:\n")
	fmt.Fprintf(&buf, "  Total: %d\n", m.total)
	fmt.Fprintf(&buf, "  Active: %d\n", m.active)
	fmt.Fprintf(&buf, "  Terminating: %d\n", m.terminating)

	buf.WriteString("\nNamespace Details:\n")
	if len(m.rows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Name", "Status", "Pods", "CPU Req/Lim", "Memory Req/Lim", "Quotas", "LimitRanges", "Created"}, m.rows)
	}

	if len(m.quotaRows) > 0 {
		buf.WriteString("\nResourceQuotas:\n")
		renderTable(&buf, []string{"Namespace", "Quota", "Resource", "Hard", "Used"}, m.quotaRows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

func formatQuantity(q resource.Quantity) string {
	if q.IsZero() {
		return "0"
	}
	return q.String()
}

func formatCPUReqLim(req, lim resource.Quantity) string {
	if req.IsZero() && lim.IsZero() {
		return "-"
	}
	return formatCPUCores(req.MilliValue()) + "/" + formatCPUCores(lim.MilliValue())
}

func formatMemoryReqLim(req, lim resource.Quantity) string {
	if req.IsZero() && lim.IsZero() {
		return "-"
	}
	return formatBytes(req.Value()) + "/" + formatBytes(lim.Value())
}
