package records

import (
	"context"
	"strings"
)

// Sequence returns, for each of ids the revision ("" is the current one)
// holds, its write position: the store's sequence number of the record, the
// order records were first written (a later version keeps its record's
// place). A document's chunks are written right after it, in order, so
// their positions follow the document's and then their place in it. Ids the
// revision does not hold are left out.
func (s *Store) Sequence(ctx context.Context, revisionHash string, ids []string) (map[string]int64, error) {
	rev, _, err := revision(ctx, s.readDB(), revisionHash)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for start := 0; start < len(ids); start += 500 {
		part := ids[start:min(start+500, len(ids))]
		args := []any{rev, rev}
		for _, id := range part {
			args = append(args, id)
		}
		rows, err := s.readDB().QueryContext(ctx, `SELECT id,seq FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?) AND id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var seq int64
			if err = rows.Scan(&id, &seq); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = seq
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
