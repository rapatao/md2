# Full image: bundles Chromium so every feature works out of the box —
# browser-fallback PDF, mermaid rendering, and -flatten. This is the default
# (:latest) tag. goreleaser drops the prebuilt md2 binary into the build
# context, so there is no in-image compile step.
#
# Debian base with glibc chromium: the best-trodden path for headless Chromium
# (fonts/rendering across locales). Alpine+chromium was measured at essentially
# the same size (~760MB vs ~810MB) because Chromium's mandatory Mesa/LLVM GL
# stack dominates regardless of base, so there is no size win to trade for
# musl's edge cases. md2 is a static CGO_ENABLED=0 binary. Fonts cover the
# emoji/CJK glyphs that trigger the browser fallback.
#
# The container runs as root on purpose: md2 launches Chromium via go-rod,
# which only adds --no-sandbox automatically when running as uid 0. Do not add
# a nonroot USER or headless Chromium will fail to start.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      chromium \
      ca-certificates \
      fonts-liberation \
      fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/*

COPY md2 /usr/local/bin/md2

WORKDIR /work
ENTRYPOINT ["md2"]
