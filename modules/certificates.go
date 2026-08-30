package modules

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Certificates struct {
	ModuleAbstract

	total      int
	expired    int
	expiring30 int
	expiring90 int

	rows [][]string
}

func (m Certificates) Name() string {
	return "Certificates"
}

func (m Certificates) ID() string {
	return strings.ToLower(m.Name())
}

func (m Certificates) Visible() bool {
	return true
}

func (m Certificates) EnableByDefault() bool {
	return true
}

// Run mirrors the behaviour of `chktls --k8s-tls --all-namespaces`: inspect
// every TLS secret and report the certificate subject, expiry, key validity,
// and whether the certificate matches the key.
func (m *Certificates) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	secrets, err := clientset.CoreV1().Secrets("").List(m.getContext(), metav1.ListOptions{FieldSelector: "type=kubernetes.io/tls"})
	if err != nil {
		return err
	}

	now := time.Now()
	m.rows = make([][]string, 0, len(secrets.Items))
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		cert, err := parseCertificate(secret.Data[corev1.TLSCertKey])
		if err != nil {
			continue
		}

		subject := cert.Subject.CommonName
		if subject == "" {
			subject = "-"
		}

		daysLeft := daysUntil(cert.NotAfter, now)
		classifyExpiry(cert.NotAfter, now, &m.expired, &m.expiring30, &m.expiring90)
		m.total++

		matches := "No"
		if certKeyMatches(cert, secret.Data[corev1.TLSPrivateKeyKey]) {
			matches = "Yes"
		}

		m.rows = append(m.rows, []string{
			secret.Namespace,
			secret.Name,
			subject,
			cert.NotAfter.Format("2006-01-02"),
			strconv.Itoa(daysLeft),
			keyValidity(secret.Data[corev1.TLSPrivateKeyKey]),
			matches,
		})
	}

	sort.Slice(m.rows, func(i, j int) bool {
		if m.rows[i][3] != m.rows[j][3] {
			return m.rows[i][3] < m.rows[j][3]
		}
		if m.rows[i][0] != m.rows[j][0] {
			return m.rows[i][0] < m.rows[j][0]
		}
		return m.rows[i][1] < m.rows[j][1]
	})

	return nil
}

func (m Certificates) Print(w io.Writer) error {
	var buf strings.Builder

	fmt.Fprintf(&buf, "TLS Certificates:\n")
	fmt.Fprintf(&buf, "  Total: %d\n", m.total)
	fmt.Fprintf(&buf, "  Expired: %d\n", m.expired)
	fmt.Fprintf(&buf, "  Expiring in 0-30 days: %d\n", m.expiring30)
	fmt.Fprintf(&buf, "  Expiring in 30-90 days: %d\n", m.expiring90)

	buf.WriteString("\nTLS Secrets:\n")
	if len(m.rows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Namespace", "Name", "Subject", "Expire At", "Days Left", "Key Validity", "Matches"}, m.rows)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

func parseCertificate(pemData []byte) (*x509.Certificate, error) {
	for {
		block, rest := pem.Decode(pemData)
		if block == nil {
			return nil, fmt.Errorf("no PEM block found")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
		pemData = rest
	}
}

func parsePrivateKey(pemData []byte) (crypto.PrivateKey, error) {
	for {
		block, rest := pem.Decode(pemData)
		if block == nil {
			return nil, fmt.Errorf("no PEM block found")
		}
		if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			return key, nil
		}
		if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return key, nil
		}
		if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
			return key, nil
		}
		pemData = rest
	}
}

func keyValidity(pemData []byte) string {
	key, err := parsePrivateKey(pemData)
	if err != nil {
		return "Invalid"
	}
	if rsaKey, ok := key.(*rsa.PrivateKey); ok {
		if err := rsaKey.Validate(); err != nil {
			return "Invalid"
		}
	}
	return "OK"
}

func certKeyMatches(cert *x509.Certificate, keyPEM []byte) bool {
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return false
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return false
	}
	certPub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return false
	}
	keyPub, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return false
	}
	return bytes.Equal(certPub, keyPub)
}

func daysUntil(t, now time.Time) int {
	return int(t.Sub(now).Hours() / 24)
}

func classifyExpiry(t, now time.Time, expired, expiring30, expiring90 *int) {
	switch {
	case t.Before(now):
		*expired++
	case t.Before(now.Add(30 * 24 * time.Hour)):
		*expiring30++
	case t.Before(now.Add(90 * 24 * time.Hour)):
		*expiring90++
	}
}
