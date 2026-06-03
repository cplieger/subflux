module subflux

go 1.26.4

require (
	github.com/cplieger/auth v0.0.0-00010101000000-000000000000
	github.com/go-webauthn/webauthn v0.17.4
	github.com/nwaples/rardecode/v2 v2.2.3
	github.com/ulikunitz/xz v0.5.15
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/crypto v0.52.0
	golang.org/x/sync v0.20.0
	modernc.org/sqlite v1.51.0
	pgregory.net/rapid v1.3.0
)

require (
	github.com/coreos/go-oidc/v3 v3.18.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.2.6 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/cplieger/auth => ../auth
