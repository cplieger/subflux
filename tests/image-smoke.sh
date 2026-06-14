#!/bin/sh
# Runtime image smoke test for subflux. Invoked by the central CI docker job:
#   sh tests/image-smoke.sh <image-ref>
#
# Starts the assembled image and waits for the container's own HEALTHCHECK to
# report "healthy" — proving the binary runs, the embedded web UI is present,
# ffmpeg/runtime deps are in the image, and the health probe works. subflux
# starts in unconfigured mode (web UI + config endpoints) with no env, so it
# should become healthy on its own.
set -eu

IMG="${1:?usage: image-smoke.sh <image-ref>}"
NAME="smoke-subflux-$$"
TIMEOUT=90

# shellcheck disable=SC2329  # invoked indirectly via trap
cleanup() {
	echo "--- container logs (tail) ---"
	docker logs "$NAME" 2>&1 | tail -40 || true
	docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "$NAME" "$IMG" >/dev/null

i=0
status=starting
while [ "$i" -lt "$TIMEOUT" ]; do
	status=$(docker inspect --format '{{ if .State.Health }}{{ .State.Health.Status }}{{ else }}no-healthcheck{{ end }}' "$NAME" 2>/dev/null || echo gone)
	case "$status" in
	healthy) echo "subflux image smoke: ok (healthy after ${i}s)"; exit 0 ;;
	unhealthy) echo "FAIL: subflux reported unhealthy"; exit 1 ;;
	no-healthcheck) echo "FAIL: image has no HEALTHCHECK to assert against"; exit 1 ;;
	gone) echo "FAIL: subflux container exited early"; exit 1 ;;
	esac
	i=$((i + 1))
	sleep 1
done
echo "FAIL: subflux did not become healthy within ${TIMEOUT}s (last status: $status)"
exit 1
