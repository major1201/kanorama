package modules

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// helmReleaseSecretType is the Secret type Helm 3 uses to store release
// metadata. k8s.io/api does not define a constant for it in v0.26.0.
const helmReleaseSecretType = "helm.sh/release.v1"

type Helm struct {
	ModuleAbstract

	rows [][]string
}

func (m Helm) Name() string {
	return "Helm"
}

func (m Helm) ID() string {
	return strings.ToLower(m.Name())
}

func (m Helm) Visible() bool {
	return true
}

func (m Helm) EnableByDefault() bool {
	return true
}

func (m *Helm) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	secrets, err := clientset.CoreV1().Secrets("").List(m.getContext(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	latest := make(map[string]*helmRelease)
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if secret.Type != helmReleaseSecretType {
			continue
		}
		rel, err := decodeHelmRelease(secret.Data["release"])
		if err != nil {
			continue
		}
		key := rel.Namespace + "/" + rel.Name
		if cur, ok := latest[key]; !ok || rel.Version > cur.Version {
			latest[key] = rel
		}
	}

	m.rows = make([][]string, 0, len(latest))
	for _, rel := range latest {
		chart := rel.Chart.Metadata.Name
		if rel.Chart.Metadata.Version != "" {
			chart = rel.Chart.Metadata.Name + "-" + rel.Chart.Metadata.Version
		}
		updated := "-"
		if !rel.Info.LastDeployed.IsZero() {
			updated = rel.Info.LastDeployed.Format("2006-01-02 15:04:05 -0700")
		}
		m.rows = append(m.rows, []string{
			rel.Name,
			rel.Namespace,
			strconv.Itoa(rel.Version),
			updated,
			rel.Info.Status,
			chart,
			rel.Chart.Metadata.AppVersion,
		})
	}

	sort.Slice(m.rows, func(i, j int) bool {
		if m.rows[i][1] != m.rows[j][1] {
			return m.rows[i][1] < m.rows[j][1]
		}
		return m.rows[i][0] < m.rows[j][0]
	})

	return nil
}

func (m Helm) Print(w io.Writer) error {
	var buf strings.Builder

	buf.WriteString("Helm Releases:\n")
	if len(m.rows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Name", "Namespace", "Revision", "Updated", "Status", "Chart", "App Version"}, m.rows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

type helmRelease struct {
	Name      string           `json:"name"`
	Namespace string           `json:"namespace"`
	Version   int              `json:"version"`
	Info      helmReleaseInfo  `json:"info"`
	Chart     helmReleaseChart `json:"chart"`
}

type helmReleaseInfo struct {
	FirstDeployed time.Time `json:"first_deployed"`
	LastDeployed  time.Time `json:"last_deployed"`
	Status        string    `json:"status"`
	Description   string    `json:"description"`
}

type helmReleaseChart struct {
	Metadata helmReleaseChartMetadata `json:"metadata"`
}

type helmReleaseChartMetadata struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	AppVersion string `json:"appVersion"`
}

// decodeHelmRelease parses a Helm 3 release from its Secret payload. Helm 3
// base64-encodes a gzip-compressed JSON release; client-go already decoded the
// Secret's outer base64 layer, leaving one base64 + gzip layer to unwrap here.
func decodeHelmRelease(data []byte) (*helmRelease, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty release payload")
	}
	compressed, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	decoded, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}

	var rel helmRelease
	if err := json.Unmarshal(decoded, &rel); err != nil {
		return nil, err
	}
	if rel.Name == "" {
		return nil, fmt.Errorf("missing release name")
	}
	return &rel, nil
}
