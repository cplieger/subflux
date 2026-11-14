package app

import (
	"context"

	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// newActivate makes a parsed configuration live: the store's activation,
// its auth record judged by guard's method guard, then the alerts each
// configuration-keyed kind retains, then the writer's section.
func newActivate(store *statedb.Store, st *status.Store, writer *mediawrite.Writer, guard func() statedb.MethodGuard) activateFunc {
	return func(ctx context.Context, a settings.Active) error {
		act := activationOf(&a)
		act.Guard = guard()
		if err := store.Activate(ctx, act); err != nil {
			return err
		}
		retainSources(st, act.Sources)
		retainProviders(st, a.Providers)
		// Last, so a refused activation leaves the writer's section as it was.
		writer.SetConfig(a.Media)
		return nil
	}
}

// newActivateAuth is the auth half of a boot that is not Active: the auth
// section in force recorded alone, judged by guard's method guard.
func newActivateAuth(store *statedb.Store, guard func() statedb.MethodGuard) func(context.Context, accounts.Config) error {
	return func(ctx context.Context, c accounts.Config) error {
		return store.ActivateAuth(ctx, accounts.MethodRecord(c), guard())
	}
}

// activationOf is the part of a the store keys coverage on, and its auth
// record.
func activationOf(a *settings.Active) statedb.Activation {
	auth := accounts.MethodRecord(a.Auth)
	return statedb.Activation{Auth: &auth, Languages: a.Languages, Sources: sourceIDs(a), IgnoredCodecs: a.Embedded.Ignored()}
}

// retainProviders keeps the provider and provider-data alerts of the
// enabled providers in cfgs and clears the rest, so a disabled or removed
// provider's alert never outlives it and a later raise of it is dropped.
func retainProviders(st *status.Store, cfgs []provider.Config) {
	var calls, data []status.AlertSource
	for i := range cfgs {
		if id := string(cfgs[i].ID); cfgs[i].Enabled {
			calls = append(calls, status.ProviderAlert(id))
			data = append(data, status.ProviderDataAlert(id))
		}
	}
	st.RetainAlerts(status.ProviderAlerts, calls)
	st.RetainAlerts(status.ProviderDataAlerts, data)
}
