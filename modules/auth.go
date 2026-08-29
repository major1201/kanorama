package modules

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	authenticationv1 "k8s.io/api/authentication/v1"
	authenticationv1alpha1 "k8s.io/api/authentication/v1alpha1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type Auth struct {
	ModuleAbstract

	userInfo   *authenticationv1.UserInfo
	whoAmIErr  error
	permErr    error
	permStatus *authorizationv1.SubjectRulesReviewStatus
}

func (m Auth) Name() string {
	return "Auth"
}

func (m Auth) ID() string {
	return strings.ToLower(m.Name())
}

func (m Auth) Visible() bool {
	return true
}

func (m Auth) EnableByDefault() bool {
	return true
}

func (m *Auth) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	m.whoAmIErr = m.loadWhoAmI(ctx, clientset)
	m.permErr = m.loadPermissions(ctx, clientset)

	// Only fail the whole module when neither part could be loaded; otherwise
	// print the partial result and surface the failing part in the report.
	if m.whoAmIErr != nil && m.permErr != nil {
		return fmt.Errorf("whoami: %v; permissions: %v", m.whoAmIErr, m.permErr)
	}
	return nil
}

func (m *Auth) loadWhoAmI(ctx context.Context, clientset kubernetes.Interface) error {
	review, err := clientset.AuthenticationV1alpha1().SelfSubjectReviews().Create(ctx, &authenticationv1alpha1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	m.userInfo = &review.Status.UserInfo
	return nil
}

func (m *Auth) loadPermissions(ctx context.Context, clientset kubernetes.Interface) error {
	// SelfSubjectRulesReview only evaluates a single namespace, so query every
	// namespace and merge the results to build the full permission picture.
	var namespaces []string
	nsList, nsErr := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if nsErr != nil {
		namespaces = []string{m.currentNamespace()}
	} else {
		for _, ns := range nsList.Items {
			namespaces = append(namespaces, ns.Name)
		}
	}

	status := authorizationv1.SubjectRulesReviewStatus{}
	if nsErr != nil {
		status.Incomplete = true
		status.EvaluationError = fmt.Sprintf("failed to list namespaces (%v); showing only namespace %q", nsErr, namespaces[0])
	}

	seenResource := make(map[string]bool)
	seenNonResource := make(map[string]bool)
	succeeded := 0
	var firstErr error

	for _, ns := range namespaces {
		review, err := clientset.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, &authorizationv1.SelfSubjectRulesReview{
			Spec: authorizationv1.SelfSubjectRulesReviewSpec{Namespace: ns},
		}, metav1.CreateOptions{})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			status.Incomplete = true
			continue
		}
		succeeded++

		for _, rule := range review.Status.ResourceRules {
			key := resourceRuleKey(rule)
			if !seenResource[key] {
				seenResource[key] = true
				status.ResourceRules = append(status.ResourceRules, rule)
			}
		}
		for _, rule := range review.Status.NonResourceRules {
			key := nonResourceRuleKey(rule)
			if !seenNonResource[key] {
				seenNonResource[key] = true
				status.NonResourceRules = append(status.NonResourceRules, rule)
			}
		}
		if review.Status.Incomplete {
			status.Incomplete = true
		}
		if review.Status.EvaluationError != "" {
			status.EvaluationError = review.Status.EvaluationError
		}
	}

	if succeeded == 0 && len(namespaces) > 0 {
		return firstErr
	}

	sort.Slice(status.ResourceRules, func(i, j int) bool {
		a, b := status.ResourceRules[i], status.ResourceRules[j]
		if x, y := strings.Join(a.APIGroups, ","), strings.Join(b.APIGroups, ","); x != y {
			return x < y
		}
		if x, y := strings.Join(a.Resources, ","), strings.Join(b.Resources, ","); x != y {
			return x < y
		}
		return strings.Join(a.Verbs, ",") < strings.Join(b.Verbs, ",")
	})
	sort.Slice(status.NonResourceRules, func(i, j int) bool {
		a, b := status.NonResourceRules[i], status.NonResourceRules[j]
		if x, y := strings.Join(a.NonResourceURLs, ","), strings.Join(b.NonResourceURLs, ","); x != y {
			return x < y
		}
		return strings.Join(a.Verbs, ",") < strings.Join(b.Verbs, ",")
	})

	m.permStatus = &status
	return nil
}

func (m *Auth) currentNamespace() string {
	ns, _, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).Namespace()
	if err != nil || ns == "" {
		return metav1.NamespaceDefault
	}
	return ns
}

func resourceRuleKey(r authorizationv1.ResourceRule) string {
	verbs := append([]string(nil), r.Verbs...)
	sort.Strings(verbs)
	groups := append([]string(nil), r.APIGroups...)
	sort.Strings(groups)
	resources := append([]string(nil), r.Resources...)
	sort.Strings(resources)
	names := append([]string(nil), r.ResourceNames...)
	sort.Strings(names)
	return strings.Join(verbs, ",") + "|" + strings.Join(groups, ",") + "|" + strings.Join(resources, ",") + "|" + strings.Join(names, ",")
}

func nonResourceRuleKey(r authorizationv1.NonResourceRule) string {
	verbs := append([]string(nil), r.Verbs...)
	sort.Strings(verbs)
	urls := append([]string(nil), r.NonResourceURLs...)
	sort.Strings(urls)
	return strings.Join(verbs, ",") + "|" + strings.Join(urls, ",")
}

func (m Auth) Print(w io.Writer) error {
	var buf bytes.Buffer
	buf.WriteString("Auth:\n")

	if m.userInfo != nil {
		ui := m.userInfo
		buf.WriteString("  User:\n")
		fmt.Fprintf(&buf, "    Username: %s\n", ui.Username)
		if ui.UID != "" {
			fmt.Fprintf(&buf, "    UID: %s\n", ui.UID)
		}
		if len(ui.Groups) > 0 {
			fmt.Fprintf(&buf, "    Groups: %v\n", ui.Groups)
		}
		if len(ui.Extra) > 0 {
			keys := make([]string, 0, len(ui.Extra))
			for k := range ui.Extra {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&buf, "    Extra: %s=%v\n", k, ui.Extra[k])
			}
		}
	} else {
		buf.WriteString("  User: unavailable\n")
		if m.whoAmIErr != nil {
			fmt.Fprintf(&buf, "    error: %v\n", m.whoAmIErr)
		}
	}

	if m.permErr != nil {
		buf.WriteString("  Permissions: unavailable\n")
		fmt.Fprintf(&buf, "    error: %v\n", m.permErr)
		_, err := w.Write(buf.Bytes())
		return err
	}

	status := m.permStatus
	if status == nil {
		buf.WriteString("  Permissions: unavailable\n")
		_, err := w.Write(buf.Bytes())
		return err
	}
	if status.Incomplete {
		if status.EvaluationError != "" {
			fmt.Fprintf(&buf, "  Permissions: incomplete (%s)\n", status.EvaluationError)
		} else {
			buf.WriteString("  Permissions: incomplete\n")
		}
	}

	buf.WriteString("  Resource Rules:\n")
	tw := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "    APIGroups\tResources\tResource Names\tVerbs")
	for _, r := range status.ResourceRules {
		fmt.Fprintf(tw, "    %q\t%q\t%q\t%q\n", r.APIGroups, r.Resources, r.ResourceNames, r.Verbs)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	buf.WriteString("  Non-Resource Rules:\n")
	tw = tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "    Verbs\tNon-Resource URLs")
	for _, r := range status.NonResourceRules {
		fmt.Fprintf(tw, "    %q\t%q\n", r.Verbs, r.NonResourceURLs)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	_, err := w.Write(buf.Bytes())
	return err
}
