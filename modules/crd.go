package modules

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type CRD struct {
	ModuleAbstract

	rows [][]string
}

func (m CRD) Name() string {
	return "CRD"
}

func (m CRD) ID() string {
	return strings.ToLower(m.Name())
}

func (m CRD) Visible() bool {
	return true
}

func (m CRD) EnableByDefault() bool {
	return true
}

func (m *CRD) Run() error {
	config, err := newRestConfig()
	if err != nil {
		return err
	}
	apiextClient, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return err
	}
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}
	ctx := m.getContext()

	crdList, err := apiextClient.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	m.rows = make([][]string, 0, len(crdList.Items))
	for i := range crdList.Items {
		crd := &crdList.Items[i]
		version := crdServedVersion(crd)
		apiVersion := crd.Spec.Group + "/" + version
		namespaced := "false"
		if crd.Spec.Scope == apiextensionsv1.NamespaceScoped {
			namespaced = "true"
		}

		count := "-"
		if version != "" {
			if n, err := countCRInstances(ctx, dynClient, crd, version); err == nil {
				count = strconv.Itoa(n)
			}
		}

		m.rows = append(m.rows, []string{
			crd.Name,
			strings.Join(crd.Spec.Names.ShortNames, ","),
			apiVersion,
			namespaced,
			crd.CreationTimestamp.Format("2006-01-02 15:04:05 -0700"),
			count,
		})
	}

	sort.Slice(m.rows, func(i, j int) bool {
		if m.rows[i][2] != m.rows[j][2] {
			return m.rows[i][2] < m.rows[j][2]
		}
		return m.rows[i][0] < m.rows[j][0]
	})

	return nil
}

func (m CRD) Print(w io.Writer) error {
	var buf strings.Builder

	buf.WriteString("CustomResourceDefinitions:\n")
	if len(m.rows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"NAME", "SHORTNAMES", "APIVERSION", "NAMESPACED", "CREATED_AT", "COUNT"}, m.rows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

// crdServedVersion returns the version to use for listing instances: the
// storage version if set, otherwise the first declared version.
func crdServedVersion(crd *apiextensionsv1.CustomResourceDefinition) string {
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			return v.Name
		}
	}
	if len(crd.Spec.Versions) > 0 {
		return crd.Spec.Versions[0].Name
	}
	return ""
}

func countCRInstances(ctx context.Context, dynClient dynamic.Interface, crd *apiextensionsv1.CustomResourceDefinition, version string) (int, error) {
	gvr := schema.GroupVersionResource{
		Group:    crd.Spec.Group,
		Version:  version,
		Resource: crd.Spec.Names.Plural,
	}

	var (
		list *unstructured.UnstructuredList
		err  error
	)
	if crd.Spec.Scope == apiextensionsv1.NamespaceScoped {
		list, err = dynClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	} else {
		list, err = dynClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return 0, err
	}
	return len(list.Items), nil
}
