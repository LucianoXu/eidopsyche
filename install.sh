#!/usr/bin/env sh
# install.sh — one-line installer / updater for the eidos binary.
#
# Canonical URL:
#     https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh
#
# Usage:
#     curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sh
#
# Environment variables:
#     PREFIX         install prefix (default: $HOME/.local)
#     EIDOS_VERSION  pin a specific tag (default: latest, e.g. v0.1.0)
#     DESTDIR        staged-install root (for packaging)
#
# This script is the single source of truth for upgrade logic. The
# `eidos self-update` command is a thin wrapper that re-executes it.

set -eu

REPO="LucianoXu/eidopsyche"
BIN_NAME="eidos"
PROJECT="eidos"

# Strict-mode quirks: nounset trips on `${X:-}` patterns under some shells; we
# use POSIX `${VAR:-default}` everywhere instead.

# ---------------------------------------------------------------- resolve OS
detect_os() {
    uname_s=$(uname -s 2>/dev/null || echo unknown)
    case "$uname_s" in
        Linux*)   echo linux ;;
        Darwin*)  echo darwin ;;
        MINGW*|MSYS*|CYGWIN*)
                  echo windows ;;
        *)        echo "unsupported OS: $uname_s" >&2; exit 1 ;;
    esac
}

detect_arch() {
    uname_m=$(uname -m 2>/dev/null || echo unknown)
    case "$uname_m" in
        x86_64|amd64)   echo amd64 ;;
        aarch64|arm64)  echo arm64 ;;
        *) echo "unsupported architecture: $uname_m" >&2; exit 1 ;;
    esac
}

# ------------------------------------------------------------- resolve tools
require() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "install.sh: missing required tool: $1" >&2
        exit 1
    fi
}

# Either curl or wget; prefer curl. Both must support TLS.
fetch_to() {
    # $1 = URL, $2 = output path
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 2 --connect-timeout 10 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$2" "$1"
    else
        echo "install.sh: need curl or wget" >&2
        exit 1
    fi
}

fetch_stdout() {
    # $1 = URL — write to stdout
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 2 --connect-timeout 10 "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O - "$1"
    else
        echo "install.sh: need curl or wget" >&2
        exit 1
    fi
}

# ------------------------------------------------------------- resolve version
resolve_version() {
    if [ -n "${EIDOS_VERSION:-}" ]; then
        echo "$EIDOS_VERSION"
        return
    fi
    api_url="https://api.github.com/repos/${REPO}/releases/latest"
    body=$(fetch_stdout "$api_url")
    # Parse `"tag_name": "vX.Y.Z"` without depending on jq.
    tag=$(printf '%s\n' "$body" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
    if [ -z "$tag" ]; then
        echo "install.sh: could not determine latest version from $api_url" >&2
        exit 1
    fi
    echo "$tag"
}

# ------------------------------------------------------------- main
main() {
    OS=$(detect_os)
    ARCH=$(detect_arch)
    VERSION=$(resolve_version)
    VERSION_NUM="${VERSION#v}"   # strip leading 'v' for archive name

    case "$OS" in
        windows) EXT="zip" ;;
        *)       EXT="tar.gz" ;;
    esac

    ARCHIVE="${PROJECT}_${VERSION_NUM}_${OS}_${ARCH}.${EXT}"
    BASE="https://github.com/${REPO}/releases/download/${VERSION}"
    ARCHIVE_URL="${BASE}/${ARCHIVE}"
    CHECKSUMS_URL="${BASE}/checksums.txt"

    PREFIX="${PREFIX:-$HOME/.local}"
    BINDIR="${DESTDIR:-}${PREFIX}/bin"

    # Tools needed beyond fetch.
    case "$EXT" in
        tar.gz) require tar ;;
        zip)    require unzip ;;
    esac
    if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
        echo "install.sh: missing required tool: sha256sum or shasum" >&2
        exit 1
    fi

    # Use a clean staging dir; trap removal on exit (success or failure).
    STAGE=$(mktemp -d 2>/dev/null || mktemp -d -t eidos-install)
    # shellcheck disable=SC2064
    trap "rm -rf '$STAGE'" EXIT INT TERM

    echo "install.sh: target  ${OS}/${ARCH}"
    echo "install.sh: version ${VERSION}"
    echo "install.sh: prefix  ${PREFIX}"
    echo "install.sh: bindir  ${BINDIR}"
    echo "install.sh: fetch   ${ARCHIVE_URL}"

    fetch_to "$ARCHIVE_URL"   "$STAGE/$ARCHIVE"
    fetch_to "$CHECKSUMS_URL" "$STAGE/checksums.txt"

    # Verify SHA256. checksums.txt is GoReleaser's standard format:
    #     <sha256>  <filename>
    expected=$(grep -E "[[:space:]]${ARCHIVE}\$" "$STAGE/checksums.txt" \
               | awk '{print $1}' | head -n1)
    if [ -z "$expected" ]; then
        echo "install.sh: $ARCHIVE not listed in checksums.txt — refusing to install" >&2
        exit 1
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$STAGE/$ARCHIVE" | awk '{print $1}')
    else
        actual=$(shasum -a 256 "$STAGE/$ARCHIVE" | awk '{print $1}')
    fi

    if [ "$expected" != "$actual" ]; then
        echo "install.sh: SHA256 mismatch — refusing to install" >&2
        echo "  expected: $expected" >&2
        echo "  actual:   $actual" >&2
        exit 1
    fi
    echo "install.sh: SHA256 ok"

    # Extract.
    case "$EXT" in
        tar.gz) tar -xzf "$STAGE/$ARCHIVE" -C "$STAGE" ;;
        zip)    unzip -q "$STAGE/$ARCHIVE" -d "$STAGE" ;;
    esac

    case "$OS" in
        windows) src="$STAGE/${BIN_NAME}.exe" ;;
        *)       src="$STAGE/${BIN_NAME}" ;;
    esac
    if [ ! -f "$src" ]; then
        echo "install.sh: ${BIN_NAME} binary not found in archive" >&2
        exit 1
    fi

    mkdir -p "$BINDIR"
    install -m 0755 "$src" "$BINDIR/$(basename "$src")"

    echo
    echo "✓ installed: $BINDIR/$(basename "$src") ($VERSION)"

    # PATH advice. Skipped under DESTDIR (staging) because the final user-facing
    # location may differ.
    if [ -z "${DESTDIR:-}" ]; then
        case ":$PATH:" in
            *":$BINDIR:"*) ;;
            *) echo "  note: $BINDIR is not on \$PATH — add it (e.g. in ~/.bashrc) to call ${BIN_NAME} directly" ;;
        esac
    fi

    # Auto-restart the gate daemon so a managed service picks up the new
    # binary without operator intervention. `gate restart --if-running`
    # is a no-op when nothing is installed (fresh-install case) and on
    # platforms with no service backend, so it is safe to invoke
    # unconditionally on linux/darwin.
    #
    # Skipped when:
    #   - EIDOS_NO_RESTART=1     (operator opt-out, also set by `eidos self-update --no-restart`)
    #   - DESTDIR is set         (staged/packaged install — no live daemon)
    #   - OS is windows          (no service backend yet, see #15)
    if [ "${EIDOS_NO_RESTART:-}" != "1" ] && [ -z "${DESTDIR:-}" ] && [ "$OS" != "windows" ]; then
        # Best-effort: a non-zero exit must not fail the install. The
        # binary is already in place; restart failures degrade UX but do
        # not corrupt the install.
        "$BINDIR/$(basename "$src")" gate restart --if-running || true
    fi
}

main "$@"
