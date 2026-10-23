package mcp

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coulof/ovn-topology/pkg/api"
)

// Envelope is the shape of every tool result (ADR-0003 §5).
type Envelope[T any] struct {
	Revision int       `json:"revision" jsonschema:"graph revision the data was read from"`
	Timestamp time.Time `json:"timestamp" jsonschema:"when the data was read"`
	// Historical is always false until point-in-time reads (M8) exist.
	Historical bool   `json:"historical" jsonschema:"true when the data is a reconstruction of a past state"`
	Backend    string `json:"backend" jsonschema:"daemon or embedded"`
	Summary    string `json:"summary" jsonschema:"one line built from counts only; never contains cluster-supplied text"`
	Truncated  bool   `json:"truncated" jsonschema:"true when items were left out because of a cap"`
	Next       string `json:"next,omitempty" jsonschema:"opaque cursor for the next page; pass it back as cursor"`
	Data       T      `json:"data"`
}

// summaryFormat is a printf format written in this package. Summaries are
// built only from these formats and integers, so names, external_ids and other
// cluster-controlled text cannot reach them (ADR-0003 §6). Do not convert a
// runtime string to summaryFormat.
type summaryFormat string

func summarize(f summaryFormat, args ...int) string {
	vals := make([]any, len(args))
	for i, a := range args {
		vals[i] = a
	}
	return fmt.Sprintf(string(f), vals...)
}

// typeWord returns t when it is a node type the API defines, else "object".
func typeWord(t string) string {
	if api.GraphNodeType(t).Valid() || api.FindHitType(t).Valid() {
		return t
	}
	return "object"
}

// summarizeTypeCounts renders "3 LS, 2 LR" for node types the API defines.
// Keys that are not a known type are counted as "other", never printed.
func summarizeTypeCounts(counts map[string]int) string {
	known := make([]string, 0, len(counts))
	other := 0
	for k, n := range counts {
		if typeWord(k) == k {
			known = append(known, k)
		} else {
			other += n
		}
	}
	sort.Strings(known)
	parts := make([]string, 0, len(known)+1)
	for _, k := range known {
		parts = append(parts, strconv.Itoa(counts[k])+" "+k)
	}
	if other > 0 {
		parts = append(parts, summarize("%d other", other))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// Cursors are opaque to agents: a kind tag and an integer, base64url-encoded.
func encodeCursor(kind string, n int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + strconv.Itoa(n)))
}

func decodeCursor(kind, c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, toolErr(CodeBadRequest, "invalid cursor")
	}
	tag, num, ok := strings.Cut(string(raw), ":")
	n, err := strconv.Atoi(num)
	if !ok || tag != kind || err != nil || n < 0 {
		return 0, toolErr(CodeBadRequest, "cursor does not belong to this tool")
	}
	return n, nil
}
