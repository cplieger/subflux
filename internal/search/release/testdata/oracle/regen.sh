#!/usr/bin/env bash
# Regenerates testdata/dotnet_oracle.json — the .NET-oracle corpus results
# for the release PCRE layer (spec subflux-release-parse-fidelity R2).
#
# One-shot, NOT a build/CI dependency: the JSON output is committed and the
# Go comparison test (oracle_test.go) verifies staleness hashes against the
# live pattern tables and corpus. Rerun this script whenever formats.go
# patterns or corpus.json change (the comparison test fails loudly until
# you do).
#
# The .NET SDK image is pinned by MAJOR + DIGEST (R2.2). To move to a newer
# digest, update ORACLE_IMAGE below and commit the regenerated JSON with it.
set -euo pipefail

cd "$(dirname "$0")"

# mcr.microsoft.com/dotnet/sdk:10.0 as of 2026-07-19 (index digest).
ORACLE_IMAGE="mcr.microsoft.com/dotnet/sdk:10.0@sha256:ed034a8bf0b24ded0cbbac07e17825d8e9ebfe21e308191d0f7421eaf5ad4664"

# 1. Export the live pattern tables from the Go source of truth.
(cd ../../../../.. && UPDATE_ORACLE_PATTERNS=1 go test -count=1 -run TestOracleExportPatterns ./internal/search/release/)

# 2. Run the checked-in .NET 10 file-based runner against patterns + corpus.
# The testdata dir is mounted (":z" relabels for SELinux hosts) so the
# committed output lands at testdata/dotnet_oracle.json. The container runs
# as the invoking user so the output is not root-owned; the SDK gets a
# writable throwaway HOME for its build artifacts.
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/..:/oracle:z" \
  -w /oracle/oracle \
  -e HOME=/tmp \
  -e DOTNET_CLI_HOME=/tmp \
  -e XDG_DATA_HOME=/tmp/.local/share \
  -e DOTNET_CLI_TELEMETRY_OPTOUT=1 \
  -e DOTNET_NOLOGO=1 \
  "$ORACLE_IMAGE" \
  dotnet run oracle.cs -- patterns.json corpus.json ../dotnet_oracle.json

echo "regenerated $(cd .. && pwd)/dotnet_oracle.json"
