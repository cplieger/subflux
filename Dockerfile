# check=error=true

# --- Build args (global - declared before any FROM so all stages can adopt
# the default via a bare `ARG NAME` inside the stage) ---
ARG FFMPEG_VERSION=8.1
# renovate: datasource=git-refs depName=https://code.videolan.org/videolan/x264.git currentValue=stable
ARG X264_COMMIT=4613ac3c

# --- Source downloads (cached independently) ---
FROM alpine:3.23.4@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11 AS sources

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
ARG FFMPEG_VERSION=8.1
ARG X264_COMMIT
# Alpine package versions are implicitly pinned via the base-image digest
# above; pinning each apk package separately drifts faster than it helps
# (mirrors the DL3008 convention used in apps/vibekit and apps/vibecli).
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

# --- Minimal ffmpeg build (audio/video decode, subtitle, 360p preview encode) ---
# Audio decode + subtitle decode for sync pipeline.
# Video decode + x264 encode + scale filter for 360p preview transcode.
# Produces ~5MB ffmpeg + ~2MB ffprobe. No network, no HW accel.
FROM alpine:3.23.4@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11 AS ffmpeg-builder

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# hadolint ignore=DL3018
RUN apk add --no-cache build-base yasm nasm bash pkgconf clang lld gcc musl-dev linux-headers

# Build x264 as a static library (Alpine has no x264-static package).
COPY --from=sources /tmp/x264-stable /tmp/x264-stable
WORKDIR /tmp/x264-stable
RUN export CC=clang \
    && bash ./configure --enable-static --disable-cli --disable-opencl \
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

# --- TypeScript build (compile static-src/*.ts → static/*.js) ---
# Uses the same tsgo (Microsoft's typescript-go native preview) tarball
# pattern as apps/vibekit and apps/vibecli; renovate tracks the npm package
# @typescript/native-preview's `latest` dist-tag (Microsoft's curated stabler
# channel) rather than the daily `latest` channel — the platform-specific
# linux-x64 tarball is published in lockstep at the same version string.
# Plain alpine here (not golang-alpine) because nothing in this stage needs
# Go — tsgo is a self-contained native binary. See .github/renovate.json
# for the followTag rule.
FROM alpine:3.23.4@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11 AS ts-builder
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates curl

# renovate: datasource=npm depName=@typescript/native-preview
ARG TSGO_VERSION=7.0.0-dev.20260527.2
RUN curl -fsSL \
      "https://registry.npmjs.org/@typescript/native-preview-linux-x64/-/native-preview-linux-x64-${TSGO_VERSION}.tgz" \
    | tar -xz -C /tmp

WORKDIR /src/static-src
COPY internal/server/static-src/ ./

# Fetch @cplieger/actions and @cplieger/reactive TS source from npm registry
# so tsgo can resolve the `import ... from "@cplieger/<lib>"` statements at
# build time. Each lib publishes TS source only — same pattern as vibekit /
# vibecli. Extracted to static-src/node_modules/@cplieger/<lib>/ so tsgo's
# bundler resolution finds the package + its types.
# renovate: datasource=npm depName=@cplieger/actions
ARG CPLIEGER_ACTIONS_VERSION=1.1.1
RUN mkdir -p node_modules/@cplieger/actions && \
    curl -fsSL "https://registry.npmjs.org/@cplieger/actions/-/actions-${CPLIEGER_ACTIONS_VERSION}.tgz" \
      | tar -xz -C node_modules/@cplieger/actions --strip-components=1
# renovate: datasource=npm depName=@cplieger/reactive
ARG CPLIEGER_REACTIVE_VERSION=1.0.2
RUN mkdir -p node_modules/@cplieger/reactive && \
    curl -fsSL "https://registry.npmjs.org/@cplieger/reactive/-/reactive-${CPLIEGER_REACTIVE_VERSION}.tgz" \
      | tar -xz -C node_modules/@cplieger/reactive --strip-components=1

# Compile app TypeScript and the @cplieger lib TS source in a single layer.
# App TS emits to ../static via tsconfig.json's outDir; lib TS emits to
# ../static/vendor/<scope>-<lib>/ for the importmap-based browser resolution
# (see internal/server/static/index.html).
RUN /tmp/package/lib/tsgo --project tsconfig.json && \
    /tmp/package/lib/tsgo \
        --ignoreConfig --module ESNext --target ESNext --moduleResolution bundler \
        --outDir ../static/vendor/cplieger-actions \
        --rootDir node_modules/@cplieger/actions/src \
        --skipLibCheck --strict \
        node_modules/@cplieger/actions/src/*.ts && \
    /tmp/package/lib/tsgo \
        --ignoreConfig --module ESNext --target ESNext --moduleResolution bundler \
        --outDir ../static/vendor/cplieger-reactive \
        --rootDir node_modules/@cplieger/reactive/src \
        --skipLibCheck --strict \
        node_modules/@cplieger/reactive/src/*.ts

# --- Go build ---
FROM golang:1.26-alpine@sha256:f23e8b227fb4493eabe03bede4d5a32d04092da71962f1fb79b5f7d1e6c2a17f AS builder
ENV GOTOOLCHAIN=auto

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY *.go ./
COPY config.example.yaml ./
COPY internal/ internal/
COPY --from=ts-builder /src/static/*.js internal/server/static/

# Concatenate per-feature CSS splits into the served bundles.
# Naming convention:
#   MANIFEST          -> style.css  (the main bundle, like vibekit/vibecli)
#   <name>.MANIFEST   -> <name>.css (e.g. login.MANIFEST -> login.css)
RUN set -eu; \
    css_src=internal/server/static-src/css; \
    css_out=internal/server/static; \
    for manifest in "${css_src}"/*MANIFEST; do \
        mname=$(basename "${manifest}"); \
        if [ "${mname}" = "MANIFEST" ]; then \
            out_name="style.css"; \
        else \
            out_name="${mname%.MANIFEST}.css"; \
        fi; \
        : > "${css_out}/${out_name}"; \
        while IFS= read -r line || [ -n "$line" ]; do \
            case "$line" in ''|\#*) continue ;; esac; \
            cat "${css_src}/${line}" >> "${css_out}/${out_name}"; \
        done < "${manifest}"; \
    done

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o /subflux .

# --- Final image ---
FROM gcr.io/distroless/static-debian13:nonroot@sha256:963fa6c544fe5ce420f1f54fb88b6fb01479f054c8056d0f74cc2c6000df5240

COPY --from=ffmpeg-builder --chmod=755 /tmp/ffmpeg/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-builder --chmod=755 /tmp/ffmpeg/ffprobe /usr/local/bin/ffprobe
COPY --from=builder --chmod=755 /subflux /subflux
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --retries=3 --start-period=15s \
    CMD ["/subflux", "health"]
ENTRYPOINT ["/subflux"]
