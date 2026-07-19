package boltstore

// This file is the migration REGISTRY: the two append-only domain ladders the
// framework in migrate.go executes at Open, and the domain descriptors that
// bind each ladder to its stamp key, current version, and population probe.
//
// # How to ship a breaking schema change (post-v1)
//
//  1. Bump the domain's version constant in codec.go (coreSchemaVersion or
//     authSchemaVersion) by exactly one.
//  2. Append ONE step to the domain's ladder below, migrating from the old
//     version to the new one:
//     - a targeted in-place transform when possible:
//     {from: N, to: N + 1, kind: migrateInPlace, run: <transform>};
//     - the destructive fallback when a targeted transform is impossible —
//     core: {..., run: resetCorePreserving} (preserves manual rows and
//     sync offsets by construction); auth: a step body implemented in
//     internal/authstore over its ResetPreserving seam (the auth record
//     types are unexported there by design), registered here as
//     {..., run: authstore.MigrationVxToVy};
//     - a copy-to-new-file rewrite for format-level changes:
//     {from: N, to: N + 1, kind: migrateCopy, transform: <transform>}.
//  3. Never edit or remove an existing step (users may still be on any older
//     version), never touch the other domain's stamp inside a step (the
//     framework verifies this and rolls a violating step back), and keep the
//     ladder contiguous — validateLadder refuses gaps, duplicates, non-unit
//     steps, a first step that does not start at the domain's base version,
//     a terminal version that is not the constant, and an EMPTY ladder once
//     the version constant has moved past the base (a bump must register its
//     path).
//
// Steps run inside Open with a pre-migration snapshot already written; see
// migrate.go for the execution and crash-resume contract.
//
// # Alpha posture
//
// Both ladders are EMPTY by design (user directive 2026-07-18): the framework
// ships as future-production infrastructure, both stamps stay at their current
// values, and no alpha change registers a real step. Until v1, breaking store
// changes may still reset dev databases freely; the framework exists so
// post-v1 bumps never have to. The machinery is proven by injected test
// ladders (migrate_test.go and siblings), which never mutate these registries.
var (
	// coreMigrations is the core-domain ladder (search_attempts,
	// subtitle_state, subtitle_files, scan_state, sync_offsets, poll_state and
	// their indexes). Empty: coreSchemaVersion has never been bumped.
	coreMigrations []migration

	// authMigrations is the auth-domain ladder (auth_users, auth_passkeys,
	// auth_api_keys and their indexes). Empty: authSchemaVersion has never
	// been bumped.
	authMigrations []migration
)

// coreDomain returns the production core-domain descriptor: the
// core_schema_version stamp, this build's core version, the registered core
// ladder, and the core data buckets (everything in coreBuckets except meta)
// as the populated-file probe.
func coreDomain() *migrationDomain {
	return &migrationDomain{
		name:        "core",
		stampKey:    metaKeyCoreSchemaVersion,
		current:     coreSchemaVersion,
		base:        coreSchemaBaseVersion,
		ladder:      coreMigrations,
		dataBuckets: coreDataBuckets(),
	}
}

// authDomain returns the production auth-domain descriptor: the
// auth_schema_version stamp, this build's auth version, the registered auth
// ladder, and the seven auth buckets as the populated-file probe.
func authDomain() *migrationDomain {
	return &migrationDomain{
		name:        "auth",
		stampKey:    metaKeyAuthSchemaVersion,
		current:     authSchemaVersion,
		base:        authSchemaBaseVersion,
		ladder:      authMigrations,
		dataBuckets: authBuckets,
	}
}

// coreDataBuckets returns the core buckets that can hold domain DATA: every
// bucket in coreBuckets except meta. meta is excluded from the population
// probe because it belongs to both domains (it carries both stamps) and its
// counters describe rows, not data of their own.
func coreDataBuckets() [][]byte {
	out := make([][]byte, 0, len(coreBuckets)-1)
	for _, b := range coreBuckets {
		if string(b) == bucketMeta {
			continue
		}
		out = append(out, b)
	}
	return out
}
