#!/usr/bin/env bash
# Builds portable, self-contained frp-console executables per platform.
# Each output embeds that platform's frpc/frps (gzipped), so the exe alone is
# enough: on first run it creates conf/, profiles/, logs/, bin/ next to itself.
set -euo pipefail

cd "$(dirname "$0")"

VERSION="${VERSION:-0.5.1}"
FRP_VERSION="${FRP_VERSION:-0.71.1}"
OUT="${OUT:-dist}"
CACHE="$OUT/.cache"
EMBED_DIR="internal/embedfs/bin"
# GitHub release assets are stalled in some networks; fall back to a mirror
# prefix. Set FRP_MIRROR="" to disable, or FRP_MIRROR="https://..." to override.
FRP_MIRROR="${FRP_MIRROR-https://ghfast.top/}"

# Stated scope: Linux (dev host) + Windows (test target).
# Add macOS once its frp release zip is in dist/.cache (or network allows):
#   TARGETS+=("darwin arm64")
TARGETS=(
  "linux amd64"
  "windows amd64"
)

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }
}
need go
need curl
need unzip
need gzip
need tar

# download <url> <dest>: direct first, then mirror; aborts stalled transfers.
download() {
  local url="$1" dest="$2"
  local attempt
  local prefixes=("")
  [[ -n "$FRP_MIRROR" ]] && prefixes+=("$FRP_MIRROR")
  for attempt in "${prefixes[@]}"; do
    echo ">> download ${attempt}${url}" >&2
    if curl -fL --retry 2 --connect-timeout 10 --speed-limit 4096 --speed-time 25 \
        --max-time 600 -o "$dest.tmp" "${attempt}${url}"; then
      mv "$dest.tmp" "$dest"
      return 0
    fi
    rm -f "$dest.tmp"
  done
  echo "download failed: $url" >&2
  return 1
}

# fetch_frp <os> <arch> -> prints the extracted release directory
fetch_frp() {
  local os="$1" arch="$2"
  local dir="$CACHE/frp_${FRP_VERSION}_${os}_${arch}"
  local archive ext
  # frp ships .zip for Windows, .tar.gz for every other platform.
  if [[ "$os" == "windows" ]]; then
    ext="zip"
  else
    ext="tar.gz"
  fi
  archive="$CACHE/frp_${FRP_VERSION}_${os}_${arch}.${ext}"

  if [[ -x "$dir/frpc" || -f "$dir/frpc.exe" ]]; then
    echo "$dir"
    return
  fi

  mkdir -p "$CACHE"
  if [[ ! -f "$archive" ]]; then
    download \
      "https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/frp_${FRP_VERSION}_${os}_${arch}.${ext}" \
      "$archive"
  fi
  rm -rf "$dir"
  case "$archive" in
    *.zip) unzip -q -o "$archive" -d "$CACHE" ;;
    *.tar.gz) tar -xzf "$archive" -C "$CACHE" ;;
  esac
  echo "$dir"
}

rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
restore_placeholder() {
  rm -rf "$EMBED_DIR"
  mkdir -p "$EMBED_DIR"
  : > "$EMBED_DIR/.gitkeep"
}
trap restore_placeholder EXIT

for target in "${TARGETS[@]}"; do
  read -r os arch <<<"$target"
  frpdir="$(fetch_frp "$os" "$arch")"

  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"

  # Stage only this target's binaries: the embed pattern globs the whole dir.
  rm -rf "$EMBED_DIR"
  mkdir -p "$EMBED_DIR"
  gzip -9 -c "$frpdir/frpc$ext" > "$EMBED_DIR/frpc$ext.gz"
  gzip -9 -c "$frpdir/frps$ext" > "$EMBED_DIR/frps$ext.gz"

  outdir="$OUT/frp-console_${os}_${arch}"
  mkdir -p "$outdir"
  # frp ships under Apache-2.0; keep its license next to the embedded binaries.
  cp "$frpdir/LICENSE" "$outdir/LICENSE-frp.txt"
  # windowsgui = no console window; the tray becomes the visible surface.
  # Set WINDOWS_CONSOLE=1 to build a console variant for debugging.
  ldflags="-s -w -X main.version=${VERSION}"
  if [[ "$os" == "windows" && "${WINDOWS_CONSOLE:-0}" != "1" ]]; then
    ldflags="$ldflags -H=windowsgui"
  fi

  echo ">> build ${os}/${arch} -> ${outdir}/frp-console${ext}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "$ldflags" \
    -o "$outdir/frp-console${ext}" .
done

echo
echo "done:"
ls -lh "$OUT"/frp-console_*/frp-console* 2>/dev/null
