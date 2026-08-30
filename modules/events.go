package modules

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const recentWarningsLimit = 20

type Events struct {
	ModuleAbstract

	total      int
	byType     map[string]int
	byReason   map[string]int
	reasonType map[string]string
	byKind     map[string]int
	warnings   [][]string
}

func (m Events) Name() string {
	return "Events"
}

func (m Events) ID() string {
	return strings.ToLower(m.Name())
}

func (m Events) Visible() bool {
	return true
}

// Events are volatile and can be very noisy on large clusters, so this module
// is opt-in rather than part of the default report.
func (m Events) EnableByDefault() bool {
	return false
}

func (m *Events) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	list, err := clientset.CoreV1().Events("").List(m.getContext(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	m.byType = make(map[string]int)
	m.byReason = make(map[string]int)
	m.reasonType = make(map[string]string)
	m.byKind = make(map[string]int)

	warnings := make([]corev1.Event, 0)
	for i := range list.Items {
		e := &list.Items[i]
		m.total++
		m.byType[e.Type]++
		m.byReason[e.Reason]++
		if t, ok := m.reasonType[e.Reason]; ok && t != e.Type {
			m.reasonType[e.Reason] = "Mixed"
		} else {
			m.reasonType[e.Reason] = e.Type
		}
		m.byKind[e.InvolvedObject.Kind]++
		if e.Type == corev1.EventTypeWarning {
			warnings = append(warnings, *e)
		}
	}

	sort.Slice(warnings, func(i, j int) bool {
		ti, tj := eventTimestamp(warnings[i]), eventTimestamp(warnings[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return warnings[i].InvolvedObject.Name < warnings[j].InvolvedObject.Name
	})

	limit := recentWarningsLimit
	if len(warnings) < limit {
		limit = len(warnings)
	}
	m.warnings = make([][]string, 0, limit)
	for _, e := range warnings[:limit] {
		m.warnings = append(m.warnings, []string{
			eventTimestamp(e).Format("2006-01-02 15:04:05 -0700"),
			e.InvolvedObject.Namespace,
			e.InvolvedObject.Kind,
			e.InvolvedObject.Name,
			e.Reason,
			truncateString(e.Message, 80),
		})
	}

	return nil
}

func (m Events) Print(w io.Writer) error {
	var buf strings.Builder

	fmt.Fprintf(&buf, "Events:\n")
	fmt.Fprintf(&buf, "  Total: %d\n", m.total)
	fmt.Fprintf(&buf, "  Normal: %d\n", m.byType[corev1.EventTypeNormal])
	fmt.Fprintf(&buf, "  Warning: %d\n", m.byType[corev1.EventTypeWarning])

	buf.WriteString("\nBy Reason:\n")
	reasonRows := topReasonRows(m.byReason, m.reasonType, 10)
	if len(reasonRows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Type", "Reason", "Count"}, reasonRows)
	}

	buf.WriteString("\nBy Involved Kind:\n")
	kindRows := topCountRows(m.byKind, 10)
	if len(kindRows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Kind", "Count"}, kindRows)
	}

	buf.WriteString("\nRecent Warnings:\n")
	if len(m.warnings) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Time", "Namespace", "Kind", "Name", "Reason", "Message"}, m.warnings)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

func eventTimestamp(e corev1.Event) time.Time {
	if !e.LastTimestamp.Time.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.Time.IsZero() {
		return e.EventTime.Time
	}
	if !e.FirstTimestamp.Time.IsZero() {
		return e.FirstTimestamp.Time
	}
	return time.Time{}
}

func topCountRows(counts map[string]int, limit int) [][]string {
	keys := sortedIntKeys(counts)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{k, strconv.Itoa(counts[k])})
	}
	return rows
}

func topReasonRows(counts map[string]int, types map[string]string, limit int) [][]string {
	keys := sortedIntKeys(counts)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{types[k], k, strconv.Itoa(counts[k])})
	}
	return rows
}

func truncateString(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
