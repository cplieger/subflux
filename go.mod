module github.com/cplieger/subflux

go 1.26.5

require (
	// arrapi v1.7.0 is UNPUBLISHED (latest tag: v1.6.0): it ships the
	// capture-side StatusError body sanitization via runesafe. See go.work
	// for the local dev resolution until the tag lands.
	github.com/cplieger/arrapi v1.7.5
	github.com/cplieger/atomicfile/v2 v2.3.0
	github.com/cplieger/auth/v2 v2.1.1
	github.com/cplieger/health v1.4.0
	// runesafe v1.1.0 is published (Untrusted provenance type +
	// IsUnsafeNonASCII); resolved from the proxy, no go.work rider.
	github.com/cplieger/runesafe v1.2.0
	github.com/cplieger/slogx v1.4.0
	github.com/cplieger/ssrf/v3 v3.0.0
	github.com/cplieger/webhttp v1.10.0
	github.com/cplieger/wiregen/v2 v2.0.0
	github.com/go-webauthn/webauthn v0.17.4
	github.com/nwaples/rardecode/v2 v2.2.5
	github.com/ulikunitz/xz v0.5.16
	go.etcd.io/bbolt v1.5.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/sync v0.22.0
	pgregory.net/rapid v1.3.0
)

require github.com/cplieger/envx/yamlenv v1.2.0

require github.com/cplieger/metrics/v3 v3.0.0

require github.com/cplieger/httpx/v3 v3.2.0

require github.com/cplieger/jsonx v1.2.0

require github.com/evanw/esbuild v0.28.1

require golang.org/x/term v0.45.0

require (
	github.com/coreos/go-oidc/v3 v3.20.0 // indirect
	github.com/cplieger/envx v1.2.2
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.2.6 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)
