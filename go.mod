module github.com/cplieger/subflux

go 1.27.2

require (
	github.com/cplieger/arrapi/v2 v2.2.3
	github.com/cplieger/atomicfile/v4 v4.1.0
	github.com/cplieger/auth/v6 v6.2.0
	github.com/cplieger/envx/v2 v2.0.7
	github.com/cplieger/envx/yamlenv/v2 v2.0.3
	github.com/cplieger/health v1.8.2
	github.com/cplieger/httpx/v5 v5.0.5
	github.com/cplieger/jsonx/v2 v2.0.3
	github.com/cplieger/keyenc v1.1.0-dev.1
	github.com/cplieger/langtag/v2 v2.1.0-dev.1
	github.com/cplieger/metrics/v4 v4.1.0
	github.com/cplieger/pathinside/v2 v2.0.3
	github.com/cplieger/runesafe/v3 v3.0.0
	github.com/cplieger/slogx v1.6.7
	github.com/cplieger/sse v1.2.0
	github.com/cplieger/ssrf/v4 v4.3.0
	github.com/cplieger/webhttp/v3 v3.0.2
	github.com/cplieger/wiregen/v3 v3.3.0
	github.com/cplieger/xmlx v1.0.6
	github.com/evanw/esbuild v0.28.2
	github.com/nwaples/rardecode/v2 v2.4.2
	github.com/ulikunitz/xz v0.5.17
	go.etcd.io/bbolt v1.5.0
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/net v0.61.0
	golang.org/x/sync v0.24.0
	golang.org/x/term v0.47.0
	pgregory.net/rapid v1.3.0
)

require (
	github.com/coreos/go-oidc/v3 v3.21.0 // indirect
	github.com/cplieger/runesafe/v2 v2.1.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.6 // indirect
	github.com/go-jose/go-jose/v4 v4.1.5 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/webauthn v0.18.2 // indirect
	github.com/go-webauthn/x v0.3.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
)

ignore ./internal/server/static-src/node_modules
