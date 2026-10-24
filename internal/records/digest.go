package records

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
)

// The revision digest of a store is sha256("[" + join(",", record JSON of the current records in seq order) + "]").
// Every revision hash, revision/<hash> key and ExpectedRevision depends on that value, so it never changes. Put
// keeps the SHA-256 state of the digest's input without its closing "]" in meta, and a Put that only appends
// records hashes them onto that state instead of reloading and rehashing every current record.
//
// The state is a cache of the digest, never its authority: readDigestState hands it out only when it belongs to the
// current revision and finishes to the stored digest, and every other Put recomputes the digest from the current
// records and rewrites the state. Older builds ignore the two keys, so after one of them wrote the store the state
// describes an earlier revision and the next Put of a newer build takes the slow path once.
const (
	digestStateKey         = "digest/state"          // base64 of the marshaled SHA-256 state
	digestStateRevisionKey = "digest/state-revision" // the revision the state belongs to, in decimal
)

// nextDigest returns the digest of revision rev+1, whose records Put has written to tx, and saves the digest state
// of that revision. A Put that only appended records (appendOnly) hashes them, in seq order, onto the saved state of
// revision rev, whose digest is digest. Any other Put, and an append whose saved state cannot be trusted, rehashes
// the current records.
func nextDigest(ctx context.Context, tx *sql.Tx, rev int64, digest string, appendOnly bool, appended []record) (string, error) {
	if !appendOnly {
		return recomputeDigest(ctx, tx, rev+1)
	}
	h, ok, err := readDigestState(ctx, tx, rev, digest)
	if err != nil {
		return "", err
	}
	if !ok {
		return recomputeDigest(ctx, tx, rev+1)
	}
	// emptyRevision is the digest of no records: the first record of an empty store takes no comma.
	first := digest == emptyRevision
	for _, r := range appended {
		if !first {
			h.Write([]byte(","))
		}
		h.Write([]byte(r.Raw))
		first = false
	}
	if err = writeDigestState(ctx, tx, h, rev+1); err != nil {
		return "", err
	}
	return finishDigest(h)
}

// recomputeDigest is the slow path: it hashes every current record of revision rev and saves the state.
func recomputeDigest(ctx context.Context, tx *sql.Tx, rev int64) (string, error) {
	all, err := loadCurrent(ctx, tx, rev)
	if err != nil {
		return "", err
	}
	raws := make([]string, len(all))
	for i, r := range all {
		raws[i] = r.Raw
	}
	h := digestPrefix(raws)
	if err = writeDigestState(ctx, tx, h, rev); err != nil {
		return "", err
	}
	return finishDigest(h)
}

// digestPrefix hashes the digest's input up to, but not including, its closing "]": "[" + join(",", raws).
func digestPrefix(raws []string) hash.Hash {
	h := sha256.New()
	h.Write([]byte("["))
	for i, raw := range raws {
		if i > 0 {
			h.Write([]byte(","))
		}
		h.Write([]byte(raw))
	}
	return h
}

// finishDigest returns the digest for the input h has hashed so far, closed by "]". It hashes the "]" into a copy,
// so h can go on taking records.
func finishDigest(h hash.Hash) (string, error) {
	state, err := marshalDigest(h)
	if err != nil {
		return "", fmt.Errorf("digest state: %w", err)
	}
	closed, err := resumeDigest(state)
	if err != nil {
		return "", err
	}
	closed.Write([]byte("]"))
	return hex.EncodeToString(closed.Sum(nil)), nil
}

// readDigestState returns the hash resumed from the saved digest state, ready for the records a Put appends, when
// that state is the one of revision rev, whose digest is digest. It reports false, not an error, for a state it
// cannot trust: absent, labelled with another revision, undecodable, or not finishing to digest. The error is the
// database's.
func readDigestState(ctx context.Context, tx *sql.Tx, rev int64, digest string) (hash.Hash, bool, error) {
	label, ok, err := metaText(ctx, tx, digestStateRevisionKey)
	if err != nil || !ok || label != strconv.FormatInt(rev, 10) {
		return nil, false, err
	}
	saved, ok, err := metaText(ctx, tx, digestStateKey)
	if err != nil || !ok {
		return nil, false, err
	}
	// A state that does not decode, or that does not reproduce the stored digest, is damaged or stale. It costs a
	// recomputation, which rewrites it; it never reaches the digest.
	state, err := base64.StdEncoding.DecodeString(saved)
	if err != nil {
		return nil, false, nil
	}
	h, err := resumeDigest(state)
	if err != nil {
		return nil, false, nil
	}
	if finished, err := finishDigest(h); err != nil || finished != digest {
		return nil, false, nil
	}
	return h, true, nil
}

// writeDigestState saves the state of h as the digest state of revision rev.
func writeDigestState(ctx context.Context, tx *sql.Tx, h hash.Hash, rev int64) error {
	state, err := marshalDigest(h)
	if err != nil {
		return fmt.Errorf("save digest state: %w", err)
	}
	for _, entry := range [][2]string{
		{digestStateKey, base64.StdEncoding.EncodeToString(state)},
		{digestStateRevisionKey, strconv.FormatInt(rev, 10)},
	} {
		if _, err := tx.ExecContext(ctx, upsertMeta, entry[0], entry[1]); err != nil {
			return fmt.Errorf("save digest state: %w", err)
		}
	}
	return nil
}

// marshalDigest returns the state of h, which crypto/sha256 documents its hashes to marshal.
func marshalDigest(h hash.Hash) ([]byte, error) {
	m, ok := h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, errors.New("sha256 hash cannot marshal its state")
	}
	return m.MarshalBinary()
}

// resumeDigest returns a new SHA-256 hash that has the state marshalDigest returned; its errors say they are the
// digest state's.
func resumeDigest(state []byte) (hash.Hash, error) {
	h := sha256.New()
	u, ok := h.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, errors.New("digest state: sha256 hash cannot unmarshal a state")
	}
	if err := u.UnmarshalBinary(state); err != nil {
		return nil, fmt.Errorf("digest state: %w", err)
	}
	return h, nil
}

// metaText reads one value of the meta table; ok is false when the key is absent.
func metaText(ctx context.Context, tx *sql.Tx, key string) (value string, ok bool, err error) {
	err = tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read meta %s: %w", key, err)
	}
	return value, true, nil
}

// upsertMeta sets one meta key.
const upsertMeta = "INSERT INTO meta VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value"
