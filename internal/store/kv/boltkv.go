// Package boltkv holds the shared bbolt store primitives used by both the
// core subflux store (internal/store) and the auth store (internal/authstore).
//
// It is a leaf package: it depends only on the standard library and
// go.etcd.io/bbolt and MUST NOT import internal/store or internal/authstore.
// The dependency DAG is:
//
//	api <- boltkv <- {store, authstore} <- {main, cli}
//
// The primitives are:
//
//   - Key encoders ([Be64], [Join]/[Split], [TimeIndexKey]) that produce keys
//     whose byte-lexicographic order matches their semantic order, so bbolt's
//     ordered cursors and Seek give chronological / insertion-order scans and
//     correct prefix queries.
//   - A versioned-JSON value codec ([Encode]/[Decode]). Values are JSON; keys
//     are binary. The schema version is a small scalar the caller stores in its
//     own meta key (the core store uses core_schema_version, the auth store
//     uses auth_schema_version); boltkv provides the codec mechanism and the
//     meta scalar helpers ([GetUint64]/[PutUint64]), the callers own the keys.
//     [DecodeOrHandle] centralises the fail-closed vs tolerant-skip policy so a
//     derived-bucket read can skip-with-warning while an auth/lock/uniqueness
//     read fails closed.
//   - The surrogate-id helper [NextID], wrapping bbolt's Bucket.NextSequence.
//   - The generic secondary-index maintenance helpers [PutIndexed] and
//     [DeleteIndexed], which keep declared [IndexSpec] index buckets and
//     declared [CounterSpec] meta counters consistent with their primary in the
//     same all-or-nothing bbolt transaction.
package boltkv

// Sep is the single NUL byte used to join the components of a composite text
// key. Key components are NUL-free UTF-8 text, so the separator unambiguously
// delimits component boundaries and keeps prefix scans correct.
const Sep byte = 0x00
