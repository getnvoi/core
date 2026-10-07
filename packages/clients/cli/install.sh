#!/bin/sh
# Installs nvoi: curl -fsSL https://cli.nvoi.to/install.sh | sh
#
# Reads the version `bin/publish-cli` wrote to `latest`, picks the binary for
# this machine, and puts it on the PATH — /usr/local/bin when writable, else
# $HOME/.local/bin.
set -eu

base="https://cli.nvoi.to"
version="$(curl -fsSL "$base/nvoi/latest")"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os/$arch" in
  darwin/arm64)   name="nvoi-darwin-arm64" ;;
  darwin/x86_64)  name="nvoi-darwin-x86_64" ;;
  linux/x86_64)   name="nvoi-linux-x86_64" ;;
  linux/aarch64)  name="nvoi-linux-aarch64" ;;
  *) echo "nvoi has no build for $os/$arch" >&2; exit 1 ;;
esac

if [ -w /usr/local/bin ]; then
  dir="/usr/local/bin"
else
  dir="$HOME/.local/bin"
  mkdir -p "$dir"
fi

curl -fsSL "$base/nvoi/$version/$name" -o "$dir/nvoi"
chmod +x "$dir/nvoi"
echo "nvoi $version installed to $dir/nvoi"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "add $dir to your PATH" ;;
esac
