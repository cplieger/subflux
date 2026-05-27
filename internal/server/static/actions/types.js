// Action framework types: defines the contract between callers, the
// dispatcher, and observers. Pure types — no imports, no runtime
// behavior — so any module in the codebase can depend on this without
// pulling in transport / toast / store.
// ---------------------------------------------------------------------------
/** Standard retry config for network-retryable actions: 2 retries, 300ms initial delay. */
export const RETRY_STANDARD = { count: 2, delay: 300 };
