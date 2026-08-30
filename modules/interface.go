package modules

import (
	"context"
	"io"
)

type Module interface {
	Name() string
	ID() string
	Init(ctx context.Context)
	Run() error
	Visible() bool
	Print(w io.Writer) error
	EnableByDefault() bool
}

type ModuleAbstract struct {
	ctx   context.Context
	cache *Cache
}

func (m *ModuleAbstract) Init(ctx context.Context) {
	m.ctx = ctx
	m.cache = sharedCache
}

func (m ModuleAbstract) getContext() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func (m ModuleAbstract) getCache() *Cache {
	if m.cache == nil {
		return sharedCache
	}
	return m.cache
}

func (m ModuleAbstract) Run() error {
	return nil
}

func (m ModuleAbstract) Print(w io.Writer) error {
	_, err := io.WriteString(w, "no implement\n")
	return err
}
