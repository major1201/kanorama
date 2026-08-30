package modules

import (
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
)

type Ingresses struct {
	ModuleAbstract

	rows [][]string
}

func (m Ingresses) Name() string {
	return "Ingresses"
}

func (m Ingresses) ID() string {
	return strings.ToLower(m.Name())
}

func (m Ingresses) Visible() bool {
	return true
}

func (m Ingresses) EnableByDefault() bool {
	return true
}

func (m *Ingresses) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	list, err := clientset.NetworkingV1().Ingresses("").List(m.getContext(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	m.rows = make([][]string, 0, len(list.Items))
	for i := range list.Items {
		ing := &list.Items[i]
		m.rows = append(m.rows, []string{
			ing.Namespace,
			ing.Name,
			ingressClass(ing),
			ingressHosts(ing.Spec.Rules),
			ingressAddress(ing.Status.LoadBalancer.Ingress),
			ingressPorts(ing.Spec.TLS),
			formatAge(ing.CreationTimestamp),
		})
	}

	sort.Slice(m.rows, func(i, j int) bool {
		if m.rows[i][0] != m.rows[j][0] {
			return m.rows[i][0] < m.rows[j][0]
		}
		return m.rows[i][1] < m.rows[j][1]
	})
	return nil
}

func (m Ingresses) Print(w io.Writer) error {
	var buf strings.Builder

	if len(m.rows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Namespace", "Name", "Class", "Hosts", "Address", "Ports", "Age"}, m.rows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

// ingressClass mirrors `kubectl get ingress`: the spec.ingressClassName value,
// or <none> when unset.
func ingressClass(ing *networkingv1.Ingress) string {
	if ing.Spec.IngressClassName != nil {
		return *ing.Spec.IngressClassName
	}
	return "<none>"
}

// ingressHosts mirrors kubectl's host formatting: up to three non-empty rule
// hosts joined by commas, "*" when there are no rules, and "+ N more..." for
// the remainder.
func ingressHosts(rules []networkingv1.IngressRule) string {
	const max = 3
	hosts := make([]string, 0, max)
	more := 0
	for _, rule := range rules {
		if rule.Host == "" {
			continue
		}
		if len(hosts) == max {
			more++
			continue
		}
		hosts = append(hosts, rule.Host)
	}
	if len(hosts) == 0 {
		return "*"
	}
	ret := strings.Join(hosts, ",")
	if more > 0 {
		return ret + " + " + strconv.Itoa(more) + " more..."
	}
	return ret
}

// ingressAddress mirrors kubectl: unique load-balancer IPs/hostnames joined by
// commas, or <none> when the ingress has no status address yet.
func ingressAddress(ingress []networkingv1.IngressLoadBalancerIngress) string {
	seen := make(map[string]bool)
	var addrs []string
	for _, point := range ingress {
		var value string
		switch {
		case point.IP != "":
			value = point.IP
		case point.Hostname != "":
			value = point.Hostname
		}
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		addrs = append(addrs, value)
	}
	if len(addrs) == 0 {
		return "<none>"
	}
	return strings.Join(addrs, ",")
}

// ingressPorts mirrors kubectl: 80 without TLS, 80, 443 with any TLS entry.
func ingressPorts(tls []networkingv1.IngressTLS) string {
	if len(tls) > 0 {
		return "80, 443"
	}
	return "80"
}

// formatAge mirrors kubectl's AGE column using the same duration rules.
func formatAge(t metav1.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	return duration.HumanDuration(time.Since(t.Time))
}
