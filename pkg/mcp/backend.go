// Package mcp exposes the topology graph to AI agents as Model Context
// Protocol tools (ADR-0003). It is Tier 0 only (ADR-0001): it must never
// import pkg/diag or remotecommand, and every tool is read-only.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coulof/ovn-topology/pkg/api"
	"github.com/coulof/ovn-topology/pkg/graph"
)

// Backend is where tools read topology from: the daemon over HTTP (backend A)
// or an in-process graph (backend B, and fixtures). Both run the same
// pkg/graph code, so their results differ only in Name.
type Backend interface {
	// Name is reported in every response: "daemon" or "embedded".
	Name() string
	Graph(ctx context.Context, q GraphQuery) (*api.GraphResponse, error)
	Node(ctx context.Context, id string) (*api.NodeDetail, error)
	Find(ctx context.Context, q FindQuery) (*api.FindResponse, error)
	Chassis(ctx context.Context) (*api.ChassisList, error)
}

// GraphQuery mirrors GET /graph.
type GraphQuery struct {
	View     string
	Focus    string
	Depth    int
	Page     int
	PageSize int
}

// FindQuery mirrors GET /find.
type FindQuery struct {
	Query  string
	Type   api.FindHitType
	Limit  int
	Offset int
}

// Error codes returned to agents. They are stable; agents may branch on them.
const (
	CodeBadRequest           = "bad_request"
	CodeNotFound             = "not_found"
	CodeNotReady             = "not_ready"
	CodeUnavailableOnBackend = "unavailable_on_backend"
	CodeBackend              = "backend_error"
)

// ToolError is a tool-level failure the agent can act on.
type ToolError struct {
	Code    string
	Message string
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

func toolErr(code, format string, args ...any) *ToolError {
	return &ToolError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// SnapshotSource is satisfied by *graph.Store.
type SnapshotSource interface {
	Current() *graph.Snapshot
	IsReady() bool
}

// StaticSource serves one fixed snapshot (fixtures, tests).
type StaticSource struct{ Snapshot *graph.Snapshot }

func (s StaticSource) Current() *graph.Snapshot { return s.Snapshot }
func (s StaticSource) IsReady() bool            { return s.Snapshot != nil }

// SnapshotBackend reads an in-process graph (backend B).
type SnapshotBackend struct {
	src SnapshotSource
}

// NewSnapshotBackend returns a backend over src.
func NewSnapshotBackend(src SnapshotSource) *SnapshotBackend {
	return &SnapshotBackend{src: src}
}

func (b *SnapshotBackend) Name() string { return "embedded" }

func (b *SnapshotBackend) current() (*graph.Snapshot, error) {
	if !b.src.IsReady() {
		return nil, toolErr(CodeNotReady, "OVSDB monitors are still synchronizing the initial snapshot; retry shortly")
	}
	return b.src.Current(), nil
}

func (b *SnapshotBackend) Graph(_ context.Context, q GraphQuery) (*api.GraphResponse, error) {
	snap, err := b.current()
	if err != nil {
		return nil, err
	}
	resp, err := snap.BuildGraph(q.View, q.Focus, q.Depth, q.Page, q.PageSize)
	if err != nil {
		return nil, graphErr(err)
	}
	return resp, nil
}

func (b *SnapshotBackend) Node(_ context.Context, id string) (*api.NodeDetail, error) {
	snap, err := b.current()
	if err != nil {
		return nil, err
	}
	d, err := snap.GetNodeDetail(id)
	if err != nil {
		return nil, graphErr(err)
	}
	return d, nil
}

func (b *SnapshotBackend) Find(_ context.Context, q FindQuery) (*api.FindResponse, error) {
	snap, err := b.current()
	if err != nil {
		return nil, err
	}
	return snap.Find(q.Query, q.Type, q.Limit, q.Offset), nil
}

func (b *SnapshotBackend) Chassis(_ context.Context) (*api.ChassisList, error) {
	snap, err := b.current()
	if err != nil {
		return nil, err
	}
	return snap.GetChassisList(), nil
}

// graphErr classifies pkg/graph errors the way pkg/server does: lookups that
// miss are not_found, everything else is a bad request.
func graphErr(err error) error {
	var te *ToolError
	if errors.As(err, &te) {
		return te
	}
	if strings.Contains(err.Error(), "not found") {
		return toolErr(CodeNotFound, "%s", err.Error())
	}
	return toolErr(CodeBadRequest, "%s", err.Error())
}

// UnavailableBackend answers every call with the setup error that prevented
// a real backend, so agents can report it instead of seeing no server.
type UnavailableBackend struct {
	name string
	err  error
}

// NewUnavailableBackend returns a backend that fails every call with err.
func NewUnavailableBackend(name string, err error) *UnavailableBackend {
	return &UnavailableBackend{name: name, err: err}
}

func (b *UnavailableBackend) Name() string { return b.name }

func (b *UnavailableBackend) fail() error { return toolErr(CodeBackend, "%v", b.err) }

func (b *UnavailableBackend) Graph(context.Context, GraphQuery) (*api.GraphResponse, error) {
	return nil, b.fail()
}

func (b *UnavailableBackend) Node(context.Context, string) (*api.NodeDetail, error) {
	return nil, b.fail()
}

func (b *UnavailableBackend) Find(context.Context, FindQuery) (*api.FindResponse, error) {
	return nil, b.fail()
}

func (b *UnavailableBackend) Chassis(context.Context) (*api.ChassisList, error) {
	return nil, b.fail()
}
