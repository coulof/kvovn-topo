package graph

import (
	"net"
	"sort"
	"strings"

	"github.com/coulof/kvovn-topo/pkg/api"
)

// FindMaxLimit caps the hits returned by one Find call.
const FindMaxLimit = 200

// findTypeOrder ranks hit types so that containers sort before their ports.
var findTypeOrder = map[api.FindHitType]int{
	api.FindHitTypeLR:      0,
	api.FindHitTypeLS:      1,
	api.FindHitTypeLRP:     2,
	api.FindHitTypeLSP:     3,
	api.FindHitTypeChassis: 4,
}

// Find searches routers, switches, ports and chassis. An IP or MAC query
// matches addresses exactly; any other query matches names, pod, VM and
// chassis hostname as a case-insensitive substring, and namespace exactly.
// Each object is reported once, with its best match: an exact match on any
// field beats a substring match, then fields rank in the order they are tried.
// typeFilter is optional; limit is clamped to [1, FindMaxLimit].
func (s *Snapshot) Find(query string, typeFilter api.FindHitType, limit, offset int) *api.FindResponse {
	if limit <= 0 {
		limit = 20
	} else if limit > FindMaxLimit {
		limit = FindMaxLimit
	}
	offset = max(offset, 0)

	m := newMatcher(query)
	hits := make([]api.FindHit, 0)
	want := func(t api.FindHitType) bool { return typeFilter == "" || typeFilter == t }
	add := func(hit api.FindHit, r match) {
		hit.MatchedField, hit.Exact = r.field, r.exact
		hits = append(hits, hit)
	}

	if want(api.FindHitTypeLR) {
		for uuid, lr := range s.LogicalRouters {
			if r, ok := best(m.text(api.Name, lr.Name)); ok {
				add(api.FindHit{Id: PrefixLR + uuid, Type: api.FindHitTypeLR, Name: lr.Name, K8s: ExtractK8sMetadata(lr.ExternalIDs)}, r)
			}
		}
	}

	if want(api.FindHitTypeLS) {
		for uuid, ls := range s.LogicalSwitches {
			if r, ok := best(m.text(api.Name, ls.Name)); ok {
				add(api.FindHit{Id: PrefixLS + uuid, Type: api.FindHitTypeLS, Name: ls.Name, K8s: ExtractK8sMetadata(ls.ExternalIDs)}, r)
			}
		}
	}

	if want(api.FindHitTypeLRP) {
		routerOf := reverseIndex(s.LRPsByRouter)
		for uuid, lrp := range s.LogicalRouterPorts {
			r, ok := best(m.text(api.Name, lrp.Name), m.mac(lrp.MAC), m.ip(api.Ip, lrp.Networks...))
			if !ok {
				continue
			}
			hit := api.FindHit{Id: PrefixLRP + uuid, Type: api.FindHitTypeLRP, Name: lrp.Name}
			if lr, ok := routerOf[uuid]; ok {
				parent := PrefixLR + lr
				hit.Parent = &parent
			}
			add(hit, r)
		}
	}

	if want(api.FindHitTypeLSP) {
		switchOf := reverseIndex(s.LSPsBySwitch)
		for uuid, lsp := range s.LogicalSwitchPorts {
			macs, ips := lspAddresses(lsp.Addresses, lsp.DynamicAddresses)
			k8s := ExtractK8sMetadata(lsp.ExternalIDs)
			candidates := []match{m.text(api.Name, lsp.Name), m.mac(macs...), m.ip(api.Ip, ips...)}
			if k8s != nil {
				candidates = append(candidates, m.exact(api.Namespace, k8s.Namespace), m.textPtr(api.Pod, k8s.Pod), m.textPtr(api.Vm, k8s.Vmi))
			}
			r, ok := best(candidates...)
			if !ok {
				continue
			}
			hit := api.FindHit{Id: PrefixLSP + uuid, Type: api.FindHitTypeLSP, Name: lsp.Name, K8s: k8s}
			if ls, ok := switchOf[uuid]; ok {
				parent := PrefixLS + ls
				hit.Parent = &parent
			}
			add(hit, r)
		}
	}

	if want(api.FindHitTypeChassis) {
		for _, ch := range s.Chassis {
			encapIPs := make([]string, 0, len(ch.Encaps))
			for _, e := range ch.Encaps {
				if enc, found := s.Encaps[e]; found {
					encapIPs = append(encapIPs, enc.IP)
				}
			}
			node := ""
			if s.NodeMapper != nil {
				node = s.NodeMapper.NodeForChassis(ch.Hostname, ch.Name)
			}
			r, ok := best(m.text(api.Name, ch.Name), m.text(api.Hostname, ch.Hostname), m.text(api.Node, node), m.ip(api.EncapIp, encapIPs...))
			if ok {
				add(api.FindHit{Id: PrefixChassis + ch.Name, Type: api.FindHitTypeChassis, Name: ch.Hostname}, r)
			}
		}
	}

	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Exact != b.Exact {
			return a.Exact
		}
		if findTypeOrder[a.Type] != findTypeOrder[b.Type] {
			return findTypeOrder[a.Type] < findTypeOrder[b.Type]
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Id < b.Id
	})

	total := len(hits)
	start := min(offset, total)
	end := min(start+limit, total)
	return &api.FindResponse{Revision: int(s.Revision), Total: total, Items: hits[start:end]}
}

// match is the outcome of testing one field against the query.
type match struct {
	field api.FindHitMatchedField
	exact bool
	ok    bool
}

// best returns the first exact match, else the first substring match.
func best(ms ...match) (match, bool) {
	var first *match
	for i := range ms {
		if !ms[i].ok {
			continue
		}
		if ms[i].exact {
			return ms[i], true
		}
		if first == nil {
			first = &ms[i]
		}
	}
	if first == nil {
		return match{}, false
	}
	return *first, true
}

// matcher holds a normalized query: an IP or MAC when it parses as one,
// otherwise lower-cased text. Address queries never match text fields.
type matcher struct {
	q    string
	ipq  net.IP
	macq net.HardwareAddr
}

func newMatcher(q string) *matcher {
	q = strings.TrimSpace(q)
	m := &matcher{q: strings.ToLower(q)}
	if ip := net.ParseIP(q); ip != nil {
		m.ipq = ip
	} else if mac, err := net.ParseMAC(q); err == nil {
		m.macq = mac
	}
	return m
}

func (m *matcher) isAddr() bool { return m.ipq != nil || m.macq != nil }

func (m *matcher) text(field api.FindHitMatchedField, v string) match {
	if m.isAddr() || m.q == "" || v == "" {
		return match{}
	}
	lv := strings.ToLower(v)
	switch {
	case lv == m.q:
		return match{field: field, exact: true, ok: true}
	case strings.Contains(lv, m.q):
		return match{field: field, ok: true}
	}
	return match{}
}

func (m *matcher) textPtr(field api.FindHitMatchedField, v *string) match {
	if v == nil {
		return match{}
	}
	return m.text(field, *v)
}

// exact matches a field only when it equals the query.
func (m *matcher) exact(field api.FindHitMatchedField, v *string) match {
	if r := m.textPtr(field, v); r.exact {
		return r
	}
	return match{}
}

func (m *matcher) mac(vs ...string) match {
	if m.macq == nil {
		return match{}
	}
	for _, v := range vs {
		if hw, err := net.ParseMAC(v); err == nil && hw.String() == m.macq.String() {
			return match{field: api.Mac, exact: true, ok: true}
		}
	}
	return match{}
}

// ip matches plain addresses and CIDR-notated ones (LRP networks).
func (m *matcher) ip(field api.FindHitMatchedField, vs ...string) match {
	if m.ipq == nil {
		return match{}
	}
	for _, v := range vs {
		ip := net.ParseIP(v)
		if ip == nil {
			ip, _, _ = net.ParseCIDR(v)
		}
		if ip != nil && ip.Equal(m.ipq) {
			return match{field: field, exact: true, ok: true}
		}
	}
	return match{}
}

// lspAddresses splits LSP "MAC IP…" entries (and dynamic_addresses) into MACs
// and IPs. Special values such as "router", "unknown" and "dynamic" are skipped.
func lspAddresses(addresses []string, dynamic *string) (macs, ips []string) {
	all := addresses
	if dynamic != nil && *dynamic != "" {
		all = append(append([]string(nil), addresses...), *dynamic)
	}
	for _, a := range all {
		for _, f := range strings.Fields(a) {
			if _, err := net.ParseMAC(f); err == nil {
				macs = append(macs, f)
			} else if net.ParseIP(f) != nil {
				ips = append(ips, f)
			} else if ip, _, err := net.ParseCIDR(f); err == nil {
				ips = append(ips, ip.String())
			}
		}
	}
	return macs, ips
}

// reverseIndex inverts a parent → children index into child → parent.
func reverseIndex(idx map[string][]string) map[string]string {
	out := make(map[string]string)
	for parent, children := range idx {
		for _, c := range children {
			out[c] = parent
		}
	}
	return out
}
