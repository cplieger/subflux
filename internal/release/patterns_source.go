package release

//go:generate go run ../../cmd/release-patterns generate
//go:generate env UPDATE_GOLDEN=1 go test -count=1 -run ^TestGoldenLabels$ .

// The commits patterns_gen.go was generated from; go generate fetches every
// manifest entry at them. Each const stays a single declaration under its
// renovate comment, the shape the Renovate manager matches.

// renovate: datasource=git-refs depName=trash-guides packageName=https://github.com/TRaSH-Guides/Guides branch=master
const trashCommit = "14a4166673996301e588a4f70d03b9cfb760b50f"

// renovate: datasource=git-refs depName=sonarr-parser packageName=https://github.com/Sonarr/Sonarr branch=develop
const sonarrCommit = "57816c149d02f95543973fefb4780c4495f8121c"

// renovate: datasource=git-refs depName=radarr-parser packageName=https://github.com/Radarr/Radarr branch=develop
const radarrCommit = "af7814f7a89ea46f21522f0a1b8bcdcf7644b8bf"
