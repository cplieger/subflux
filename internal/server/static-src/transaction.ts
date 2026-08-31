// transaction.ts — which SSE transaction is open, if any.
//
// events.ts opens one transaction per transacting epoch and closes it on commit
// or abort. Several collections take part, and each asks the same question at
// its own moment: coverage's tombstones ask whether a full-pair write BEGAN
// inside one, history's page leg asks whether the reload already in flight was
// dispatched inside THIS one. One fact, so one owner — a flag per collection,
// set from the same call site, is the shape that drifts when a new close path
// is added.
//
// The ID is what makes history's question answerable. A run dispatched under a
// transaction that has since aborted read data older than the transaction open
// now, so "some transaction was open" is not enough to join a read on.
//
// A LEAF: zero subflux imports, so any participant can reach it.

let openID: number | null = null;
let counter = 0;

/** Open a transaction (events.ts, once per transacting epoch). */
export function beginTransaction(): void {
  counter += 1;
  openID = counter;
}

/** Close the open transaction — commit and abort alike. Idempotent. */
export function settleTransaction(): void {
  openID = null;
}

/** The open transaction's id, or null when none is open. */
export function openTransaction(): number | null {
  return openID;
}
