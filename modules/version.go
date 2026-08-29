package modules

import (
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/version"
)

type Version struct {
	ModuleAbstract
	info *version.Info
}

func (m Version) Name() string {
	return "Version"
}

func (m Version) ID() string {
	return strings.ToLower(m.Name())
}

func (m Version) Visible() bool {
	return true
}

func (m Version) EnableByDefault() bool {
	return true
}

func (m *Version) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}

	info, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return err
	}
	m.info = info
	return nil
}

func (m Version) Print(w io.Writer) error {
	if m.info == nil {
		_, err := io.WriteString(w, "API Server Version: unknown\n")
		return err
	}
	_, err := fmt.Fprintf(w, "API Server Version: %s\n", m.info.GitVersion)
	return err
}
