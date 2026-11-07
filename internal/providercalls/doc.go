// Package providercalls is the one gate every subtitle-provider call passes.
// A credential refused three times is disabled durably, a rate-limit answer
// pauses the provider's op, five transient failures in ten minutes cool the
// provider down for automatic calls, and each provider has two in-flight
// slots, interactive waiters first, with automatic calls spaced by the scan
// delay. The state lives in one Funnel per process, by provider and by a
// digest of the credential, so an engine rebuilt on a settings save keeps it
// and a call under an old credential never changes the new one's state.
package providercalls
