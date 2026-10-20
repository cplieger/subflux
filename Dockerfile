# check=error=true

# --- Build args (global - declared before any FROM so all stages can adopt
# the default via a bare `ARG NAME` inside the stage) ---
ARG FFMPEG_VERSION=9.0.2
# x264 publishes NO tags and no versioned artifact (only the master/stable/
# GPLv2-only branches; VideoLAN's snapshot tarballs stopped in 2019), so the
# commit is the only identifier upstream offers. The marker below is the
# git-refs datasource's digest mode: `branch=` is the ref Renovate resolves and
# the ARG holds the commit it resolved to, as a FULL 40-char sha so Renovate's
# digest comparison is exact. Keep the marker immediately above the ARG.
# renovate: datasource=git-refs depName=videolan/x264 packageName=https://code.videolan.org/videolan/x264.git branch=stable
ARG X264_COMMIT=b35605ace3ddf7c1a5d67a2eb553f034aef41d55

# --- Source downloads (cached independently) ---
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS sources

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
ARG FFMPEG_VERSION
ARG X264_COMMIT
# Alpine package versions are implicitly pinned via the base-image digest
# above; pinning each apk package separately drifts faster than it helps
# (mirrors the DL3008 convention used in apps/marotte and apps/web-terminal-kiro).
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
    && rm /tmp/ffmpeg.tar.gz \
    && mkdir -p /out/usr/share/licenses/ffmpeg /out/usr/share/licenses/x264 \
    && cp /tmp/ffmpeg/COPYING.* /tmp/ffmpeg/LICENSE.md /out/usr/share/licenses/ffmpeg/ \
    && cp /tmp/x264-stable/COPYING /out/usr/share/licenses/x264/

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
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS ffmpeg-builder

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
# Subtitle decoders: SRT, ASS, MOV text, raw text, WebVTT, PGS, DVD, DVB.
# Video encoder: libx264 only (360p ultrafast preview). Audio encoder: AAC + PCM.
# Filters: scale (resize), aresample, aformat. Muxer: MP4 (fMP4 streaming).
#
# configure DROPS an --enable-<kind>= name it does not know, without failing and
# (when a sibling in the same list matched) without warning, so the assertions
# after `make` read the built binary back. They use RUNTIME names, which are not
# always the configure name: decoder `movtext` reports as `mov_text`, and the
# `pcm_s16le` muxer reports as `s16le`.
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
        --enable-decoder=subrip,srt,ass,ssa,movtext,text,webvtt,pgssub,dvdsub,dvbsub \
        --enable-encoder=libx264,aac,pcm_s16le,srt,ass,webvtt \
        --enable-parser=h264,hevc,av1,vp9,mpeg4video,mpegvideo,aac,aac_latm,ac3,mpegaudio,flac,opus,vorbis,dca \
        --enable-muxer=mp4,srt,ass,webvtt,pcm_s16le,null \
        --enable-protocol=file,pipe \
    && make -j"$(nproc)" \
    && cp ffmpeg_g ffmpeg \
    && cp ffprobe_g ffprobe \
    && test "$(./ffmpeg -hide_banner -decoders | grep -cE '^ [A-Z.]{6} (subrip|srt|ass|ssa|mov_text|text|webvtt|pgssub|dvdsub|dvbsub) ')" = 10 \
    && test "$(./ffmpeg -hide_banner -encoders | grep -cE '^ [A-Z.]{6} (libx264|aac|pcm_s16le|srt|ass|webvtt) ')" = 6 \
    && test "$(./ffmpeg -hide_banner -muxers | grep -cE '^  E  (mp4|srt|ass|webvtt|s16le|null) ')" = 6 \
    && test "$(./ffmpeg -hide_banner -filters | grep -cE '^ ..? (aresample|anull|aformat|scale) ')" = 4

# --- Go build ---
FROM golang:1.27-alpine@sha256:7d5cbf6833f7331dafd25a2e8b9673477f559759ff8ed4ca8efabe6795ad08db AS builder
ENV GOTOOLCHAIN=auto

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY cmd/subflux/ cmd/subflux/
COPY internal/ internal/

# The release workflow passes the tag. The -X path must be go.mod's module
# path: a wrong one is discarded silently and ships the "dev" default.
ARG BUILD_VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w -X github.com/cplieger/subflux/internal/obs.buildVersion=${BUILD_VERSION}" -o /subflux ./cmd/subflux \
    && mkdir -p /config-skel

COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md ./
COPY scripts/collect-licenses.sh scripts/
RUN --mount=type=cache,target=/go/pkg/mod \
    sh scripts/collect-licenses.sh --name subflux ./cmd/subflux

# --- Final image ---
FROM gcr.io/distroless/static-debian13:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087

COPY --from=ffmpeg-builder --chmod=755 /tmp/ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-builder --chmod=755 /tmp/ffmpeg/ffprobe /usr/local/bin/ffprobe
COPY --from=sources /out/usr/share/licenses /usr/share/licenses
# CycloneDX SBOM fragment for the source-built ffmpeg + statically linked
# libx264 (generated in the sources stage from the same version ARGs the
# fetches use). Placed where the release pipeline's Syft sbom-cataloger
# inventories it, so SBOMs and scanners see both components alongside the
# Go module inventory.
COPY --from=sources /tmp/subflux-ffmpeg.cdx.json /usr/share/sbom/subflux-ffmpeg.cdx.json
COPY --from=builder --chmod=755 /subflux /subflux
COPY --from=builder /out/usr/share/licenses /usr/share/licenses
# Ship an empty, nonroot-owned /config so the image starts standalone (e.g. the
# CI image smoke test) with no mount; the first settings save writes
# config.yaml there. In production /config is a bind mount. 65532 is the
# distroless nonroot uid/gid.
COPY --from=builder --chown=65532:65532 /config-skel /config
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --retries=3 --start-period=15s \
    CMD ["/subflux", "health"]
ENTRYPOINT ["/subflux"]
