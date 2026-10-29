package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/coulof/kvovn-topo/pkg/api"
)

// HTTPBackend reads the daemon API (backend A), either directly or through
// the Kubernetes API server's service proxy.
type HTTPBackend struct {
	base   string
	client *http.Client
}

// NewHTTPBackend returns a backend for the daemon API rooted at baseURL
// (ending in /api/v1). client carries the authentication; nil means
// http.DefaultClient.
func NewHTTPBackend(baseURL string, client *http.Client) *HTTPBackend {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPBackend{base: strings.TrimRight(baseURL, "/"), client: client}
}

func (b *HTTPBackend) Name() string { return "daemon" }

func (b *HTTPBackend) Graph(ctx context.Context, q GraphQuery) (*api.GraphResponse, error) {
	v := url.Values{}
	setNonEmpty(v, "view", q.View)
	setNonEmpty(v, "focus", q.Focus)
	setPositive(v, "depth", q.Depth)
	setPositive(v, "page", q.Page)
	setPositive(v, "pageSize", q.PageSize)
	var out api.GraphResponse
	return &out, b.get(ctx, "/graph", v, &out)
}

func (b *HTTPBackend) Node(ctx context.Context, id string) (*api.NodeDetail, error) {
	var out api.NodeDetail
	return &out, b.get(ctx, "/node/"+url.PathEscape(id), nil, &out)
}

func (b *HTTPBackend) Find(ctx context.Context, q FindQuery) (*api.FindResponse, error) {
	v := url.Values{"q": {q.Query}}
	setNonEmpty(v, "type", string(q.Type))
	setPositive(v, "limit", q.Limit)
	setPositive(v, "offset", q.Offset)
	var out api.FindResponse
	return &out, b.get(ctx, "/find", v, &out)
}

func (b *HTTPBackend) Chassis(ctx context.Context) (*api.ChassisList, error) {
	var out api.ChassisList
	return &out, b.get(ctx, "/chassis", nil, &out)
}

func (b *HTTPBackend) get(ctx context.Context, path string, q url.Values, out any) error {
	u := b.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return toolErr(CodeBackend, "building request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return toolErr(CodeBackend, "daemon unreachable: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return toolErr(CodeBackend, "decoding %s: %v", path, err)
		}
		return nil
	}

	msg := http.StatusText(resp.StatusCode)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var apiErr api.ErrorResponse
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Message != "" {
		msg = apiErr.Message
	}
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return toolErr(CodeBadRequest, "%s", msg)
	case http.StatusNotFound:
		if apiErr.Code == "" {
			// No daemon error body: the route itself is missing (older daemon,
			// or the proxy points at another service).
			return toolErr(CodeBackend, "daemon returned 404 for %s; is the daemon older than this client?", path)
		}
		return toolErr(CodeNotFound, "%s", msg)
	case http.StatusServiceUnavailable:
		return toolErr(CodeNotReady, "%s", msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return toolErr(CodeBackend, "HTTP %d from the API server: the kubeconfig user needs services/proxy on the daemon Service", resp.StatusCode)
	default:
		return toolErr(CodeBackend, "HTTP %d for %s: %s", resp.StatusCode, path, msg)
	}
}

func setNonEmpty(v url.Values, k, s string) {
	if s != "" {
		v.Set(k, s)
	}
}

func setPositive(v url.Values, k string, n int) {
	if n > 0 {
		v.Set(k, strconv.Itoa(n))
	}
}
