#!/bin/bash

REPO="kiosvantra/metronous"
TMPDIR=$(mktemp -d)
BINARY_NAME="metronous"

# Check for required commands
if ! command -v curl >/dev/null 2>&1; then
    echo "Error: curl is required but not installed" >&2
    exit 1
fi

if ! command -v grep >/dev/null 2>&1; then
    echo "Error: grep is required but not installed" >&2
    exit 1
fi

for cmd in tar cut head sed find; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "Error: $cmd is required but not installed" >&2
        exit 1
    fi
done

if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
    echo "Error: sha256sum or shasum is required but not installed" >&2
    exit 1
fi

trap 'rm -rf "$TMPDIR"' EXIT

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo "Unsupported architecture: $ARCH" >&2
        exit 1
        ;;
esac

case "$OS" in
    linux) EXT=".tar.gz" ;;
    darwin)
        echo "Error: install.sh is not supported on macOS." >&2
        echo "Use one of these manual flows instead:" >&2
        echo "  git clone https://github.com/kiosvantra/metronous && cd metronous" >&2
        echo "  go build -o metronous ./cmd/metronous" >&2
        echo "  ./metronous init && ./metronous server --data-dir ~/.metronous/data --daemon-mode" >&2
        exit 1
        ;;
    mingw*|msys*|cygwin*|windows)
        echo "Error: install.sh is supported on Linux only." >&2
        echo "For Windows, move the extracted metronous.exe to a permanent directory and run it from PowerShell as Administrator." >&2
        exit 1
        ;;
    *)
        echo "Unsupported OS: $OS" >&2
        exit 1
        ;;
esac

# Get latest version from GitHub API with proper error handling
echo "Checking for latest version..."

# Check if curl succeeds and capture HTTP status
HTTP_STATUS=$(curl -sSL -o /dev/null -w "%{http_code}" "https://api.github.com/repos/${REPO}/releases/latest" 2>&1) || {
    echo "Error: Failed to connect to GitHub API. Check your network connection." >&2
    exit 1
}

if [ "$HTTP_STATUS" != "200" ]; then
    if [ "$HTTP_STATUS" = "403" ]; then
        echo "Error: GitHub API rate limit exceeded. Try again later or use a GitHub token." >&2
    elif [ "$HTTP_STATUS" = "404" ]; then
        echo "Error: Repository not found or no releases available." >&2
    else
        echo "Error: GitHub API returned HTTP $HTTP_STATUS" >&2
    fi
    exit 1
fi

RESPONSE=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest")

# Validate response is valid JSON containing tag_name
if ! echo "$RESPONSE" | grep -q '"tag_name"'; then
    echo "Error: Invalid response from GitHub API" >&2
    exit 1
fi

# Extract version with robust parsing - get first match only
VERSION=$(echo "$RESPONSE" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"v[^"]*"' | head -1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | sed 's/^v//')

if [ -z "$VERSION" ]; then
    echo "Error: Failed to parse version from GitHub API response" >&2
    exit 1
fi

# Validate version format (semantic versioning)
if ! echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "Error: Invalid version format: $VERSION" >&2
    exit 1
fi

echo "Latest version: v${VERSION}"

FILENAME="metronous_${VERSION}_${OS}_${ARCH}${EXT}"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${FILENAME}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt"

echo "Downloading metronous v${VERSION} for ${OS}/${ARCH}..."

# Download with explicit error handling
if ! curl -fsSL "$URL" -o "${TMPDIR}/${FILENAME}" 2>&1; then
    echo "Error: Download failed. The file may not exist for your platform/architecture." >&2
    echo "Attempted URL: $URL" >&2
    exit 1
fi

# Verify download is not empty
if [ ! -s "${TMPDIR}/${FILENAME}" ]; then
    echo "Error: Downloaded file is empty" >&2
    exit 1
fi

echo "Verifying checksum..."
if ! curl -fsSL "$CHECKSUM_URL" -o "${TMPDIR}/checksums.txt" 2>/dev/null; then
    echo "Error: Failed to download checksums.txt" >&2
    exit 1
fi

CHECKSUM_LINE=$(grep " ${FILENAME}$" "${TMPDIR}/checksums.txt" | head -1)
if [ -z "$CHECKSUM_LINE" ]; then
    echo "Error: No checksum found for ${FILENAME}" >&2
    exit 1
fi

EXPECTED_HASH=$(printf '%s\n' "$CHECKSUM_LINE" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_HASH=$(sha256sum "${TMPDIR}/${FILENAME}" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL_HASH=$(shasum -a 256 "${TMPDIR}/${FILENAME}" | cut -d' ' -f1)
else
    echo "Error: sha256sum or shasum is required to verify the download" >&2
    exit 1
fi

if [ "$EXPECTED_HASH" != "$ACTUAL_HASH" ]; then
    echo "Error: Checksum verification failed for ${FILENAME}" >&2
    exit 1
fi

# Extract to temp location
echo "Extracting..."
if ! tar -xzf "${TMPDIR}/${FILENAME}" -C "$TMPDIR" 2>&1; then
    echo "Error: Failed to extract archive. The file may be corrupted." >&2
    exit 1
fi

# Find the binary - GoReleaser places it in the archive root (no subdirectory)
BINARY_PATH="${TMPDIR}/${BINARY_NAME}"
if [ ! -f "${BINARY_PATH}" ]; then
    # Fallback: search recursively in case layout changes
    BINARY_PATH=$(find "$TMPDIR" -type f -name "$BINARY_NAME" 2>/dev/null | head -1)
fi

if [ -z "$BINARY_PATH" ]; then
    echo "Error: Binary not found in archive" >&2
    exit 1
fi

if [ ! -x "$BINARY_PATH" ]; then
    if ! chmod +x "$BINARY_PATH" 2>/dev/null; then
        echo "Error: Binary is not executable and permissions could not be fixed" >&2
        exit 1
    fi
fi

if [ ! -s "$BINARY_PATH" ]; then
    echo "Error: Binary is empty or corrupted" >&2
    exit 1
fi

# Determine installation directory and install
install_binary() {
    local target_dir="$1"
    local bin_path="${target_dir}/${BINARY_NAME}"
    
    # Try direct copy first
    if cp "$BINARY_PATH" "$bin_path" 2>&1; then
        if chmod +x "$bin_path" 2>&1; then
            echo "Installed to $bin_path"
            return 0
        else
            echo "Error: Failed to set executable permissions on $bin_path" >&2
            rm -f "$bin_path"
            return 1
        fi
    else
        echo "Error: Failed to copy binary to $bin_path" >&2
    fi
    
    # Try with sudo if not root (use portable id -u instead of $EUID)
    if [ "$(id -u)" -ne 0 ]; then
        if sudo cp "$BINARY_PATH" "$bin_path" 2>&1; then
            if sudo chmod +x "$bin_path" 2>&1; then
                echo "Installed to $bin_path (with sudo)"
                return 0
            else
                echo "Error: Failed to set executable permissions with sudo on $bin_path" >&2
                sudo rm -f "$bin_path"
                return 1
            fi
        else
            echo "Error: Failed to copy binary with sudo to $bin_path" >&2
        fi
    fi
    
    return 1
}

# Try installation locations in order of preference
INSTALLED_PATH=""

# 1. Try /usr/local/bin (standard location)
if install_binary "/usr/local/bin"; then
    INSTALLED_PATH="/usr/local/bin/${BINARY_NAME}"
fi

# 2. If not, try user's local bin
if [ -z "$INSTALLED_PATH" ]; then
    LOCAL_BIN="$HOME/.local/bin"
    if ! mkdir -p "$LOCAL_BIN" 2>&1; then
        echo "Error: Cannot create directory $LOCAL_BIN" >&2
    else
        if install_binary "$LOCAL_BIN"; then
            INSTALLED_PATH="${LOCAL_BIN}/${BINARY_NAME}"
            echo ""
            echo "IMPORTANT: Add to your PATH:"
            echo "  export PATH=\"\${HOME}/.local/bin:\$PATH\""
            echo ""
            echo "Add this line to your ~/.bashrc or ~/.zshrc to make it permanent."
        fi
    fi
fi

if [ -z "$INSTALLED_PATH" ]; then
    echo "Error: Could not install to /usr/local/bin or ~/.local/bin." >&2
    echo "Create one of those directories with write access, then retry." >&2
    exit 1
fi

# Verify binary works
if [ -z "$INSTALLED_PATH" ] || [ ! -x "$INSTALLED_PATH" ]; then
    echo "Error: Installation failed. Please check permissions and try again." >&2
    exit 1
fi

echo ""
echo "Verifying installation..."
if ! VERSION_OUTPUT=$("$INSTALLED_PATH" --version 2>&1); then
    echo "Warning: Binary exists but --version failed." >&2
    exit 1
fi
echo "$VERSION_OUTPUT"

# Add to PATH for this session if needed
if ! command -v metronous >/dev/null 2>&1; then
    export PATH="${INSTALLED_PATH%/*}:$PATH"
fi

# Run metronous install to set up the service and configure OpenCode
echo ""
echo "Setting up Metronous service..."
if "$INSTALLED_PATH" install; then
    echo ""
    echo "========================================"
    echo "Metronous installed successfully!"
    echo "========================================"
    echo ""
    echo "Restart your terminal or run:"
    echo "  export PATH=\"\${HOME}/.local/bin:\$PATH\""
    echo ""
    exit 0
else
    echo "" >&2
    echo "Binary installed to: ${INSTALLED_PATH}" >&2
    echo "Service setup failed. Run manually: ${INSTALLED_PATH} install" >&2
    exit 1
fi
