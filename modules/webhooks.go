package modules

import (
	"io"
	"sort"
	"strconv"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Webhooks struct {
	ModuleAbstract

	validatingConfigRows [][]string
	mutatingConfigRows   [][]string
}

func (m Webhooks) Name() string {
	return "Webhooks"
}

func (m Webhooks) ID() string {
	return strings.ToLower(m.Name())
}

func (m Webhooks) Visible() bool {
	return true
}

func (m Webhooks) EnableByDefault() bool {
	return true
}

type webhookInfo struct {
	failurePolicy string
	matchPolicy   string
	resources     string
}

func (m *Webhooks) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}
	ctx := m.getContext()

	vList, err := clientset.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	mList, err := clientset.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	sort.Slice(vList.Items, func(i, j int) bool { return vList.Items[i].Name < vList.Items[j].Name })
	sort.Slice(mList.Items, func(i, j int) bool { return mList.Items[i].Name < mList.Items[j].Name })

	for i := range vList.Items {
		c := &vList.Items[i]
		infos := validatingWebhookInfos(c.Webhooks)
		m.validatingConfigRows = append(m.validatingConfigRows, webhookConfigRow(c.Name, len(infos), infos, c.CreationTimestamp.Format("2006-01-02 15:04:05 -0700")))
	}
	for i := range mList.Items {
		c := &mList.Items[i]
		infos := mutatingWebhookInfos(c.Webhooks)
		m.mutatingConfigRows = append(m.mutatingConfigRows, webhookConfigRow(c.Name, len(infos), infos, c.CreationTimestamp.Format("2006-01-02 15:04:05 -0700")))
	}

	return nil
}

func (m Webhooks) Print(w io.Writer) error {
	var buf strings.Builder

	buf.WriteString("Validating Webhook Configurations:\n")
	if len(m.validatingConfigRows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Name", "Webhooks", "Resources", "Failure Policies", "Match Policies", "Created"}, m.validatingConfigRows)
	}

	buf.WriteString("\nMutating Webhook Configurations:\n")
	if len(m.mutatingConfigRows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Name", "Webhooks", "Resources", "Failure Policies", "Match Policies", "Created"}, m.mutatingConfigRows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

func validatingWebhookInfos(webhooks []admissionregistrationv1.ValidatingWebhook) []webhookInfo {
	infos := make([]webhookInfo, 0, len(webhooks))
	for i := range webhooks {
		w := &webhooks[i]
		infos = append(infos, webhookInfo{
			failurePolicy: failurePolicyString(w.FailurePolicy),
			matchPolicy:   matchPolicyString(w.MatchPolicy),
			resources:     ruleResources(w.Rules),
		})
	}
	return infos
}

func mutatingWebhookInfos(webhooks []admissionregistrationv1.MutatingWebhook) []webhookInfo {
	infos := make([]webhookInfo, 0, len(webhooks))
	for i := range webhooks {
		w := &webhooks[i]
		infos = append(infos, webhookInfo{
			failurePolicy: failurePolicyString(w.FailurePolicy),
			matchPolicy:   matchPolicyString(w.MatchPolicy),
			resources:     ruleResources(w.Rules),
		})
	}
	return infos
}

func webhookConfigRow(name string, webhookCount int, infos []webhookInfo, created string) []string {
	return []string{
		name,
		strconv.Itoa(webhookCount),
		configResources(infos),
		uniquePolicies(infos, func(w webhookInfo) string { return w.failurePolicy }),
		uniquePolicies(infos, func(w webhookInfo) string { return w.matchPolicy }),
		created,
	}
}

func configResources(infos []webhookInfo) string {
	seen := make(map[string]bool)
	var vals []string
	for _, w := range infos {
		for _, res := range strings.Split(w.resources, ",") {
			if res == "" || seen[res] {
				continue
			}
			seen[res] = true
			vals = append(vals, res)
		}
	}
	sort.Strings(vals)
	return strings.Join(vals, ",")
}

func uniquePolicies(infos []webhookInfo, pick func(webhookInfo) string) string {
	seen := make(map[string]bool)
	var vals []string
	for _, w := range infos {
		v := pick(w)
		if v == "" {
			continue
		}
		if !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	sort.Strings(vals)
	return strings.Join(vals, ",")
}

func ruleResources(rules []admissionregistrationv1.RuleWithOperations) string {
	seen := make(map[string]bool)
	var vals []string
	for _, r := range rules {
		for _, res := range r.Resources {
			if !seen[res] {
				seen[res] = true
				vals = append(vals, res)
			}
		}
	}
	sort.Strings(vals)
	return strings.Join(vals, ",")
}

func failurePolicyString(p *admissionregistrationv1.FailurePolicyType) string {
	if p == nil {
		return "Fail"
	}
	return string(*p)
}

func matchPolicyString(p *admissionregistrationv1.MatchPolicyType) string {
	if p == nil {
		return "Equivalent"
	}
	return string(*p)
}
