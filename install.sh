#!/bin/bash
set -e

REPO="kiosvantra/metronous"
INSTALL_DIR="${HOME}/go/bin"
TMPDIR=$(mktemp -d)

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

case "$OS" in
    linux) EXT=".tar.gz" ;;
    darwin) EXT=".tar.gz" ;;
    mingw*|msys*|cygwin*|windows) OS="windows"; EXT=".tar.gz" ;;
    *)
        echo "Unsupported OS: $OS"
        exit 1
        ;;
esac

# Get latest version from GitHub API
VERSION=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')

if [ -z "$VERSION" ]; then
    echo "Failed to get latest version"
    exit 1
fi

FILENAME="metronous_${VERSION}_${OS}_${ARCH}${EXT}"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${FILENAME}"

echo "Downloading metronous v${VERSION} for ${OS}/${ARCH}..."
curl -fsSL "$URL" -o "${TMPDIR}/${FILENAME}"

# Extract to temp location
tar -xzf "${TMPDIR}/${FILENAME}" -C "$TMPDIR"

# Ensure install dir exists
mkdir -p "$INSTALL_DIR"

# Move binary to install dir
mv "${TMPDIR}/metronous" "${INSTALL_DIR}/metronous"
chmod +x "${INSTALL_DIR}/metronous"

# Cleanup
rm -rf "$TMPDIR"

echo ""
echo "Metronous v${VERSION} installed to ${INSTALL_DIR}/metronous"
echo ""

# Verify version
"${INSTALL_DIR}/metronous" --version

# Check if in PATH
if [[ ":$PATH:" != *":${INSTALL_DIR}:"* ]]; then
    echo "IMPORTANT: Add Go bin to your PATH if not already present:"
    echo "  export PATH=\"\$HOME/go/bin:\$PATH\""
    echo ""
    echo "Add to ~/.bashrc or ~/.profile to make permanent."
fi
