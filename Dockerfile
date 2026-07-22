# check=error=true

# --- Build args (global - declared before any FROM so all stages can adopt
# the default via a bare `ARG NAME` inside the stage) ---
ARG FFMPEG_VERSION=8.1
# renovate: datasource=git-refs depName=https://code.videolan.org/videolan/x264.git currentValue=stable
ARG X264_COMMIT=4613ac3c

# --- Source downloads (cached independently) ---
FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS sources

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
ARG FFMPEG_VERSION=8.1
ARG X264_COMMIT
# Alpine package versions are implicitly pinned via the base-image digest
# above; pinning each apk package separately drifts faster than it helps
# (mirrors the DL3008 convention used in apps/vibekit and apps/web-terminal-kiro).
# hadolint ignore=DL3018
RUN echo "FFMPEG_VERSION=${FFMPEG_VERSION}" \
    && apk add --no-cache curl git \
    && git clone --depth 100 --branch stable --filter=blob:none \
      https://code.videolan.org/videolan/x264.git /tmp/x264-stable \
    && git -C /tmp/x264-stable checkout "${X264_COMMIT}" \
    && rm -rf /tmp/x264-stable/.git \
    && curl -fSL --connect-timeout 10 --max-time 120 --retry 3 --retry-delay 5 \
      -o /tmp/ffmpeg.tar.gz \
      "https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n${FFMPEG_VERSION}.tar.gz" \
    && tar xz -C /tmp -f /tmp/ffmpeg.tar.gz \
    && mv /tmp/FFmpeg-n${FFMPEG_VERSION} /tmp/ffmpeg \
    && rm /tmp/ffmpeg.tar.gz

# ---------------------------------------------------------------------------
# Embedded SBOM fragment. The final image is distroless with no package DB,
# so the source-built ffmpeg/ffprobe and the statically linked libx264 are
# invisible to the signed release SBOM and to vulnerability scanners.
# Generate a CycloneDX fragment from the same version ARGs the fetches above
# use — a version bump keeps the SBOM correct with zero extra maintenance —
# and ship it in the runtime image where Syft's sbom-cataloger picks it up.
# The cataloger is enabled centrally by the release pipeline (cplieger/ci);
# no per-repo .syft.yaml is needed.
# ffmpeg purl: pkg:github mirroring the tag-tarball fetch above (namespace/
# name lowercased per the purl spec; the version keeps the tag exactly as in
# the URL, n<ver>). CPE vendor:product is ffmpeg:ffmpeg per the NVD CPE
# dictionary, e.g.
# https://nvd.nist.gov/products/cpe/detail/512EDDC9-8B04-444F-BA0C-D3BA698AEAC7/
# libx264 purl: pkg:generic with a vcs_url qualifier carrying the commit pin
# — honest provenance for a commit-pinned git build (no release tarball
# exists to point at). CPE: omitted — the NVD dictionary has no
# videolan:x264 product (the only x264 CPEs are Lexmark printers), and a
# commit-pinned version string can never match NVD's version-based CPE
# criteria anyway; scanners get the purl + commit for triage instead of a
# never-matching CPE.
RUN cat > /tmp/subflux-ffmpeg.cdx.json <<EOF
{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "version": 1,
  "components": [
    {
      "bom-ref": "pkg:github/ffmpeg/ffmpeg@n${FFMPEG_VERSION}",
      "type": "application",
      "name": "ffmpeg",
      "version": "${FFMPEG_VERSION}",
      "purl": "pkg:github/ffmpeg/ffmpeg@n${FFMPEG_VERSION}",
      "cpe": "cpe:2.3:a:ffmpeg:ffmpeg:${FFMPEG_VERSION}:*:*:*:*:*:*:*"
    },
    {
      "bom-ref": "pkg:generic/x264?vcs_url=https://code.videolan.org/videolan/x264.git@${X264_COMMIT}",
      "type": "library",
      "name": "libx264",
      "version": "${X264_COMMIT}",
      "purl": "pkg:generic/x264?vcs_url=https://code.videolan.org/videolan/x264.git@${X264_COMMIT}"
    }
  ]
}
EOF

# --- Minimal ffmpeg build (audio/video decode, subtitle, 360p preview encode) ---
# Audio decode + subtitle decode for sync pipeline.
# Video decode + x264 encode + scale filter for 360p preview transcode.
# Produces ~5MB ffmpeg + ~2MB ffprobe. No network, no HW accel.
FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS ffmpeg-builder

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# hadolint ignore=DL3018
RUN apk add --no-cache build-base yasm nasm bash pkgconf clang lld gcc musl-dev linux-headers

# Build x264 as a static library (Alpine has no x264-static package).
COPY --from=sources /tmp/x264-stable /tmp/x264-stable
WORKDIR /tmp/x264-stable
# --enable-pic is required for the static aarch64 build: without it x264's
# arm64 asm/data emit non-PIC R_AARCH64_ABS64 relocations that ld.lld rejects
# when ffmpeg statically links libx264.a (--extra-ldflags="-static -fuse-ld=lld"),
# failing configure with "x264 not found using pkg-config". No-op on amd64.
RUN export CC=clang \
    && bash ./configure --enable-static --enable-pic --disable-cli --disable-opencl \
        --prefix=/usr/local \
    && make -j"$(nproc)" \
    && make install \
    && rm -rf /tmp/x264-stable

# Video decoders: H.264, H.265, AV1, VP9, VP8, MPEG-2, MPEG-4, VC-1, Theora, FLV.
# Audio decoders: AAC, AC3, EAC3, DCA (DTS), TrueHD, MLP, MP3, FLAC, Vorbis, Opus, ALAC, WMA, PCM variants.
# Subtitle decoders: SRT, ASS, MOV text, WebVTT, PGS, DVD, DVB.
# Video encoder: libx264 only (360p ultrafast preview). Audio encoder: AAC + PCM.
# Filters: scale (resize), aresample, aformat. Muxer: MP4 (fMP4 streaming).
COPY --from=sources /tmp/ffmpeg /tmp/ffmpeg
WORKDIR /tmp/ffmpeg
RUN PKG_CONFIG_PATH=/usr/local/lib/pkgconfig \
    ./configure \
        --disable-everything \
        --disable-doc --disable-htmlpages --disable-manpages \
        --disable-podpages --disable-txtpages \
        --disable-network --disable-avdevice \
        --disable-programs \
        --enable-ffmpeg --enable-ffprobe \
        --enable-small --enable-static --disable-shared \
        --enable-gpl --enable-libx264 \
        --disable-runtime-cpudetect --disable-pixelutils \
        --enable-swscale \
        --extra-ldflags="-static -fuse-ld=lld -s" \
        --enable-filter=aresample,anull,aformat,scale \
        --enable-demuxer=matroska,mov,mp3,flac,ogg,wav,aac,ac3,eac3,dts,dtshd,srt,ass,avi,mpegts,webvtt,flv \
        --enable-decoder=h264,hevc,av1,vp9,vp8,mpeg2video,mpeg4,vc1,wmv3,theora,flv \
        --enable-decoder=aac,aac_latm,ac3,eac3,dca,mp3,mp3float,flac,vorbis,opus \
        --enable-decoder=pcm_s16le,pcm_s16be,pcm_s24le,pcm_s32le,pcm_f32le \
        --enable-decoder=truehd,mlp,alac,wmav1,wmav2 \
        --enable-decoder=subrip,ass,ssa,mov_text,webvtt,pgssub,dvdsub,dvbsub \
        --enable-encoder=libx264,aac,pcm_s16le,srt,ass,webvtt \
        --enable-parser=h264,hevc,av1,vp9,mpeg4video,mpegvideo,aac,aac_latm,ac3,mpegaudio,flac,opus,vorbis,dca \
        --enable-muxer=mp4,srt,ass,webvtt,pcm_s16le,null \
        --enable-protocol=file,pipe \
    && make -j"$(nproc)" \
    && cp ffmpeg_g ffmpeg \
    && cp ffprobe_g ffprobe

# --- TypeScript type gate (tsc --noEmit over static-src) ---
# Uses the same tsc (TypeScript 7 native compiler) tarball pattern as
# apps/vibekit. Now that TS7 shipped stable, renovate tracks the `typescript`
# npm package and we fetch its per-platform native binary
# (@typescript/typescript-linux-<arch>, published in lockstep with the
# metapackage at the same version). Plain alpine here (not golang-alpine)
# because nothing in this stage needs Go — tsc is a self-contained native
# binary. This stage only TYPECHECKS (tsconfig.json is noEmit) and fetches
# the pinned @cplieger client libraries; the bundling itself happens in the
# Go builder stage via cmd/bundle (esbuild's Go API — see that stage), which
# consumes this stage's static-src tree with the fetched node_modules.
FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS ts-builder
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates curl

# renovate: datasource=npm depName=typescript
ARG TS_VERSION=7.0.2
# Arch-aware fetch: native per-arch runners build arm64 on real arm64
# hardware, so the tsc binary must match the build arch. This is an Alpine
# builder, so use `uname -m` (aarch64/x86_64); the npm platform package
# uses arm64/x64. A hardcoded x64 breaks the arm64 build — the x64 binary
# can't execute on aarch64.
RUN TS_ARCH=$([ "$(uname -m)" = "aarch64" ] && echo "arm64" || echo "x64") && \
    curl -fsSL \
      "https://registry.npmjs.org/@typescript/typescript-linux-${TS_ARCH}/-/typescript-linux-${TS_ARCH}-${TS_VERSION}.tgz" \
    | tar -xz -C /tmp

WORKDIR /src/static-src
COPY internal/server/static-src/ ./

# Fetch @cplieger/actions and @cplieger/reactive TS source from npm registry
# so tsc can resolve the `import ... from "@cplieger/<lib>"` statements at
# build time. Each lib publishes TS source only — same pattern as vibekit /
# web-terminal-kiro. Extracted to static-src/node_modules/@cplieger/<lib>/ so tsc's
# bundler resolution finds the package + its types.
# renovate: datasource=npm depName=@cplieger/actions
ARG CPLIEGER_ACTIONS_VERSION=3.1.1
# renovate: datasource=npm depName=@cplieger/reactive
ARG CPLIEGER_REACTIVE_VERSION=1.2.5
# renovate: datasource=npm depName=@cplieger/ui-primitives
ARG CPLIEGER_UI_PRIMITIVES_VERSION=3.0.0
# renovate: datasource=npm depName=@cplieger/fetch
ARG CPLIEGER_FETCH_VERSION=2.1.0

# Pin gate (client-bundle parity, the web-terminal-kiro pattern): the SERVED
# client compiles from the ARG-pinned npm tarballs below, while
# static-src/package.json pins what local dev (tsc + vitest) compiles
# against — nothing else fails when they disagree, so a manual bump that
# misses one side would silently ship a lib version dev never ran. Assert
# every ARG == its package.json pin BEFORE fetching, so the mismatch dies
# here with a named error. Renovate moves both pins in one grouped PR on the
# routine path; this gate catches the human bypass.
RUN check_pin() { \
      pkg="$1"; want="$2"; \
      got=$(sed -n "s|.*\"@cplieger/${pkg}\": \"\([^\"]*\)\".*|\1|p" package.json); \
      : "${got:?pin-gate: no @cplieger/${pkg} pin found in static-src/package.json}"; \
      if [ "$got" != "$want" ]; then \
        echo "ERROR ${pkg}-pin-mismatch: static-src/package.json pins @cplieger/${pkg} ${got} but Dockerfile ARG pins ${want}" >&2; \
        exit 1; \
      fi; \
    } && \
    check_pin actions "$CPLIEGER_ACTIONS_VERSION" && \
    check_pin reactive "$CPLIEGER_REACTIVE_VERSION" && \
    check_pin ui-primitives "$CPLIEGER_UI_PRIMITIVES_VERSION" && \
    check_pin fetch "$CPLIEGER_FETCH_VERSION"

RUN mkdir -p node_modules/@cplieger/actions && \
    curl -fsSL "https://registry.npmjs.org/@cplieger/actions/-/actions-${CPLIEGER_ACTIONS_VERSION}.tgz" \
      | tar -xz -C node_modules/@cplieger/actions --strip-components=1
RUN mkdir -p node_modules/@cplieger/reactive && \
    curl -fsSL "https://registry.npmjs.org/@cplieger/reactive/-/reactive-${CPLIEGER_REACTIVE_VERSION}.tgz" \
      | tar -xz -C node_modules/@cplieger/reactive --strip-components=1
RUN mkdir -p node_modules/@cplieger/ui-primitives && \
    curl -fsSL "https://registry.npmjs.org/@cplieger/ui-primitives/-/ui-primitives-${CPLIEGER_UI_PRIMITIVES_VERSION}.tgz" \
      | tar -xz -C node_modules/@cplieger/ui-primitives --strip-components=1
RUN mkdir -p node_modules/@cplieger/fetch && \
    curl -fsSL "https://registry.npmjs.org/@cplieger/fetch/-/fetch-${CPLIEGER_FETCH_VERSION}.tgz" \
      | tar -xz -C node_modules/@cplieger/fetch --strip-components=1

# Type gate: tsconfig.json is noEmit, so this only typechecks the app
# sources against the pinned @cplieger lib sources fetched above — a lib/app
# type conflict fails the build here, before the Go stage bundles. esbuild
# (cmd/bundle, Go builder stage) transpiles without typechecking, so this
# gate is what keeps type errors failing the image build exactly as before.
RUN /tmp/package/lib/tsc --project tsconfig.json

# --- Go build ---
FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
ENV GOTOOLCHAIN=auto

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY *.go ./
COPY config.example.yaml ./
COPY cmd/bundle/ cmd/bundle/
COPY internal/ internal/
# The pinned @cplieger client-library sources fetched (and pin-gated) in
# ts-builder: cmd/bundle resolves the bare `@cplieger/*` import specifiers
# from static-src/node_modules, exactly like local dev's npm install.
# (.dockerignore excludes the local node_modules, so this overlay is the
# only lib source — builds never depend on the dev box's tree.)
COPY --from=ts-builder /src/static-src/node_modules/ internal/server/static-src/node_modules/

# Build the browser client into internal/server/static/ with cmd/bundle
# (esbuild via its Go API — a Go library, no Node, no npm): app.ts +
# login.ts bundle to /app.js + /login.js as ESM with code splitting (shared
# modules become hashed chunks under /chunks/, cached across the login → app
# transition), the CSS manifests concatenate to style.css / login.css,
# ui-primitives.css is copied standalone, and every emitted text asset gets
# a precompressed .gz sibling the server hands to gzip-accepting clients.
# Types were already gated in ts-builder; esbuild only bundles. go:embed
# ships the result inside the binary below.
# hadolint ignore=DL3062
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go run ./cmd/bundle

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o /subflux . \
    && mkdir -p /config-skel

# --- Final image ---
FROM gcr.io/distroless/static-debian13:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

COPY --from=ffmpeg-builder --chmod=755 /tmp/ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-builder --chmod=755 /tmp/ffmpeg/ffprobe /usr/local/bin/ffprobe
# CycloneDX SBOM fragment for the source-built ffmpeg + statically linked
# libx264 (generated in the sources stage from the same version ARGs the
# fetches use). Placed where the release pipeline's Syft sbom-cataloger
# inventories it, so SBOMs and scanners see both components alongside the
# Go module inventory.
COPY --from=sources /tmp/subflux-ffmpeg.cdx.json /usr/share/sbom/subflux-ffmpeg.cdx.json
COPY --from=builder --chmod=755 /subflux /subflux
# Ship an empty, nonroot-owned /config so the image starts standalone (e.g. the
# CI image smoke test) with no mount; subflux writes its default config + DB
# there on first run. In production /config is a bind mount. 65532 is the
# distroless nonroot uid/gid.
COPY --from=builder --chown=65532:65532 /config-skel /config
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --retries=3 --start-period=15s \
    CMD ["/subflux", "health"]
ENTRYPOINT ["/subflux"]
