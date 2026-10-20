package records

import "slices"

// Native Smart Recall (internal/memory/search/recall.go, docs/design/05-pipelines.md
// §5.2) on the derived index. Records may not import internal/memory, so the
// constants are copied here.

// recallAnchors is native's per-signal anchor limit (anchorTopK).
const recallAnchors = 20

// newest is native's recency anchor signal: the indexed records by index
// time (eventAt, else the record date, else createdAt), newest first, ties
// by id, at most n. One pass keeps the n newest; the index keeps the list.
func (index *derivedIndex) newest(n int) []string {
	newer := func(a, b string) int {
		if c := index.meta[b].at.Compare(index.meta[a].at); c != 0 {
			return c
		}
		return compareID(a, b)
	}
	out := make([]string, 0, n+1)
	for _, r := range index.docs {
		if len(out) == n && newer(r.ID, out[n-1]) >= 0 {
			continue
		}
		at, _ := slices.BinarySearchFunc(out, r.ID, newer)
		out = slices.Insert(out, at, r.ID)
		if len(out) > n {
			out = out[:n]
		}
	}
	return out
}
