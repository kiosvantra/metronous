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
    darwin) EXT=".tar.gz" ;;
    mingw*|msys*|cygwin*|windows) 
        OS="windows"
        # Check if we're in a proper Unix-like shell environment with gzip support
        if ! command -v tar >/dev/null 2>&1; then
            echo "Error: Windows installer requires Git Bash, WSL, or MSYS2 with tar and gzip support" >&2
            echo "Alternatively, install via: go install github.com/kiosvantra/metronous/cmd/metronous@latest" >&2
            exit 1
        fi
        # Verify gzip support explicitly without leaving temp files
        if ! tar -tzf /dev/null 2>/dev/null; then
            echo "Error: Windows installer requires gzip support in tar" >&2
            echo "Alternatively, install via: go install github.com/kiosvantra/metronous/cmd/metronous@latest" >&2
            exit 1
        fi
        EXT=".tar.gz"
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

# Extract to temp location
echo "Extracting..."
if ! tar -xzf "${TMPDIR}/${FILENAME}" -C "$TMPDIR" 2>&1; then
    echo "Error: Failed to extract archive. The file may be corrupted." >&2
    exit 1
fi

# Find the binary - be specific about the expected path from goreleaser
# Goreleaser extracts to: metronous_<version>_<os>_<arch>/metronous
EXPECTED_DIR="${TMPDIR}/metronous_${VERSION}_${OS}_${ARCH}"
if [ -x "${EXPECTED_DIR}/${BINARY_NAME}" ]; then
    BINARY_PATH="${EXPECTED_DIR}/${BINARY_NAME}"
else
    # Fallback: search for binary in extracted files
    BINARY_PATH=$(find "$TMPDIR" -type f -name "$BINARY_NAME" -executable 2>/dev/null | head -1)
fi

if [ -z "$BINARY_PATH" ]; then
    echo "Error: Binary not found in archive" >&2
    exit 1
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
    INSTALLED_PATH="/usr/local/bin/metronous"
    echo ""
    echo "SUCCESS: Metronous installed to /usr/local/bin"
fi

# 2. If not, try user's local bin
if [ -z "$INSTALLED_PATH" ]; then
    LOCAL_BIN="$HOME/.local/bin"
    if ! mkdir -p "$LOCAL_BIN" 2>&1; then
        echo "Error: Cannot create directory $LOCAL_BIN" >&2
    else
        if install_binary "$LOCAL_BIN"; then
            INSTALLED_PATH="${LOCAL_BIN}/metronous"
            echo ""
            echo "SUCCESS: Metronous installed to ${LOCAL_BIN}"
            echo ""
            echo "IMPORTANT: Add to your PATH:"
            echo "  export PATH=\"\${HOME}/.local/bin:\$PATH\""
            echo ""
            echo "Add this line to your ~/.bashrc or ~/.zshrc to make it permanent."
        fi
    fi
fi

# 3. If still not installed, try current directory
if [ -z "$INSTALLED_PATH" ]; then
    if cp "$BINARY_PATH" "./metronous" 2>&1; then
        if chmod +x "./metronous" 2>&1; then
            INSTALLED_PATH="./metronous"
            echo ""
            echo "WARNING: Installed to current directory. Move to a location in your PATH:"
            echo "  mv ./metronous /usr/local/bin/  (requires sudo)"
            echo "  mv ./metronous ~/.local/bin/"
        else
            echo "Error: Failed to set executable permissions on ./metronous" >&2
            rm -f "./metronous"
            exit 1
        fi
    else
        echo "Error: Failed to copy binary to current directory. Check permissions." >&2
        exit 1
    fi
fi

# Verify installation
if [ -n "$INSTALLED_PATH" ] && [ -x "$INSTALLED_PATH" ]; then
    echo ""
    echo "Verifying installation..."
    # Capture both stdout and stderr, then check exit code
    if VERSION_OUTPUT=$("$INSTALLED_PATH" --version 2>&1); then
        echo "$VERSION_OUTPUT"
        echo ""
        echo "========================================"
        echo "Installation complete!"
        echo "========================================"
        echo ""
        echo "Next steps:"
        echo "  1. Run: metronous install"
        echo "  2. Restart your terminal"
        echo ""
        exit 0
    else
        echo "Warning: Binary exists but --version failed. Installation may be incomplete." >&2
        exit 1
    fi
fi

echo "Error: Installation failed. Please check permissions and try again." >&2
exit 1
