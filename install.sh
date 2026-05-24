#!/bin/sh
set -e

REPO="rainke/agent-gateway-router"
BINARY="agr"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="$HOME/.agr"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
    darwin) OS="darwin" ;;
    linux)  OS="linux" ;;
    *) echo "Error: unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Error: unsupported architecture: $ARCH"; exit 1 ;;
esac

FILENAME="${BINARY}-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${FILENAME}"

echo "Downloading ${BINARY} for ${OS}-${ARCH}..."
TMPFILE="$(mktemp)"
curl -fsSL -o "$TMPFILE" "$DOWNLOAD_URL"

# Verify download is a valid binary (not an error page)
FILE_TYPE="$(file "$TMPFILE" 2>/dev/null || true)"
case "$FILE_TYPE" in
    *"executable"*|*"ELF"*|*"Mach-O"*)
        ;;
    *)
        echo "Error: downloaded file is not a valid binary"
        rm -f "$TMPFILE"
        exit 1
        ;;
esac

chmod +x "$TMPFILE"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "$TMPFILE" "${INSTALL_DIR}/${BINARY}"
else
    echo "Installing to ${INSTALL_DIR} requires sudo..."
    sudo mv "$TMPFILE" "${INSTALL_DIR}/${BINARY}"
fi

echo "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"

# Create config directory and example config if not exists
mkdir -p "$CONFIG_DIR"
CONFIG_FILE="${CONFIG_DIR}/config.toml"
if [ ! -f "$CONFIG_FILE" ]; then
    cat > "$CONFIG_FILE" << 'TOML'
[server]
port = 9999
log_level = "info"
pid_file = "~/.agr/agr.pid"

[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/chat/completions"
api_key = "sk-xxx"
models = ["deepseek-chat"]
transformer = ["openai", "deepseek"]

[router]
default = "deepseek,deepseek-chat"
TOML
    echo "Created example config at ${CONFIG_FILE}"
    echo "Edit it with your API key before starting."
else
    echo "Config already exists at ${CONFIG_DIR}, skipping."
fi

echo ""
echo "Done! Run 'agr start' to begin."