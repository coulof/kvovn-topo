package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/coulof/ovn-topology/pkg/api"
)

// asToolError keeps a ToolError as is and reports any other error as backend_error.
func asToolError(err error) error {
	var te *ToolError
	if errors.As(err, &te) {
		return te
	}
	return toolErr(CodeBackend, "%v", err)
}

// LazyBackend resolves its real backend on the first tool call, so the MCP
// handshake never waits on the cluster. A success is kept; a failure is
// returned and retried on the next call.
type LazyBackend struct {
	name    string
	resolve func(context.Context) (Backend, error)

	mu       sync.Mutex
	resolved Backend
}

// NewLazyBackend returns a backend named name that builds itself with resolve.
func NewLazyBackend(name string, resolve func(context.Context) (Backend, error)) *LazyBackend {
	return &LazyBackend{name: name, resolve: resolve}
}

func (b *LazyBackend) Name() string { return b.name }

// Prime resolves the backend ahead of the first tool call, so setup problems
// show in the logs at startup. Like a tool call, a failure is retried later.
func (b *LazyBackend) Prime(ctx context.Context) error {
	_, err := b.get(ctx)
	return err
}

func (b *LazyBackend) get(ctx context.Context) (Backend, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.resolved != nil {
		return b.resolved, nil
	}
	r, err := b.resolve(ctx)
	if err != nil {
		return nil, asToolError(err)
	}
	b.resolved = r
	return r, nil
}

func (b *LazyBackend) Graph(ctx context.Context, q GraphQuery) (*api.GraphResponse, error) {
	r, err := b.get(ctx)
	if err != nil {
		return nil, err
	}
	return r.Graph(ctx, q)
}

func (b *LazyBackend) Node(ctx context.Context, id string) (*api.NodeDetail, error) {
	r, err := b.get(ctx)
	if err != nil {
		return nil, err
	}
	return r.Node(ctx, id)
}

func (b *LazyBackend) Find(ctx context.Context, q FindQuery) (*api.FindResponse, error) {
	r, err := b.get(ctx)
	if err != nil {
		return nil, err
	}
	return r.Find(ctx, q)
}

func (b *LazyBackend) Chassis(ctx context.Context) (*api.ChassisList, error) {
	r, err := b.get(ctx)
	if err != nil {
		return nil, err
	}
	return r.Chassis(ctx)
}
