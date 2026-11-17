#!/bin/sh
# Installs what "make check" reports as missing: Go and Node.js (with npm).
#
#   - With Homebrew available:  brew install go / node
#   - Otherwise (Linux, macOS): downloads the official builds from go.dev and
#     nodejs.org, verifies their SHA-256 checksums and unpacks them under
#     ~/.local/opt, with links in ~/.local/bin. No sudo, nothing system-wide.
#   - Windows: prints the winget commands to run.
#
# It shows the plan and asks before doing anything. Set YES=1 to skip the
# question, DRY_RUN=1 to only print what would happen.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
NODE_LINE="${NODE_LINE:-22}"            # Node.js release line to install
OPT="${TESTPLANE_TOOLS_DIR:-$HOME/.local/opt}"
BIN="$HOME/.local/bin"

need_go=0; need_node=0
sh "$here/check-prereqs.sh" go >/dev/null 2>&1 || need_go=1
sh "$here/check-prereqs.sh" ui >/dev/null 2>&1 || need_node=1
if [ "$need_go" -eq 0 ] && [ "$need_node" -eq 0 ]; then
  echo "Nothing to install: Go, Node.js and npm are already present."
  exit 0
fi

os=$(uname -s); arch=$(uname -m)
case "$os" in
  Linux) goos=linux; nodeos=linux ;;
  Darwin) goos=darwin; nodeos=darwin ;;
  MINGW*|MSYS*|CYGWIN*)
    echo "On Windows, install with winget and then reopen the terminal:"
    [ "$need_go" -eq 1 ] && echo "  winget install GoLang.Go"
    [ "$need_node" -eq 1 ] && echo "  winget install OpenJS.NodeJS.LTS"
    exit 1 ;;
  *) echo "Unsupported system '$os'. Install Go from https://go.dev/dl/ and Node.js from https://nodejs.org/ by hand."; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) goarch=amd64; nodearch=x64 ;;
  aarch64|arm64) goarch=arm64; nodearch=arm64 ;;
  *) echo "Unsupported CPU '$arch'. Install Go from https://go.dev/dl/ and Node.js from https://nodejs.org/ by hand."; exit 1 ;;
esac

method=download
command -v brew >/dev/null 2>&1 && method=brew

echo "To install:"
[ "$need_go" -eq 1 ] && echo "  - Go (latest stable)"
[ "$need_node" -eq 1 ] && echo "  - Node.js $NODE_LINE with npm"
if [ "$method" = brew ]; then
  echo "How: Homebrew (brew install)."
else
  echo "How: official downloads from go.dev and nodejs.org, checksum-verified,"
  echo "     unpacked under $OPT with links in $BIN. No sudo."
fi
if [ "${DRY_RUN:-0}" = 1 ]; then echo "(dry run: nothing was installed)"; exit 0; fi
if [ "${YES:-0}" != 1 ]; then
  if [ ! -t 0 ]; then echo "Not asking because this is not an interactive terminal. Re-run with YES=1 to proceed."; exit 1; fi
  printf "Proceed? [y/N] "; read -r answer
  case "$answer" in y|Y|yes|YES) ;; *) echo "Cancelled."; exit 1 ;; esac
fi

if [ "$method" = brew ]; then
  [ "$need_go" -eq 1 ] && brew install go
  [ "$need_node" -eq 1 ] && brew install node
  echo; sh "$here/check-prereqs.sh" all
  exit $?
fi

for tool in curl tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "This needs '$tool', which is not installed."; exit 1; }
done
if command -v sha256sum >/dev/null 2>&1; then sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else echo "This needs sha256sum or shasum to verify downloads."; exit 1; fi

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
mkdir -p "$OPT" "$BIN"

fetch_verified() { # url expected-sha256 destination
  echo "Downloading $1"
  curl -fsSL "$1" -o "$3"
  got=$(sha "$3")
  if [ -z "$2" ] || [ "$got" != "$2" ]; then
    echo "Checksum check failed for $1"; echo "  expected: ${2:-<not found>}"; echo "  got:      $got"; exit 1
  fi
}

if [ "$need_go" -eq 1 ]; then
  ver=$(curl -fsSL "https://go.dev/VERSION?m=text" | head -n 1)
  file="$ver.$goos-$goarch.tar.gz"
  # The release index lists each file followed by its sha256.
  want=$(curl -fsSL "https://go.dev/dl/?mode=json" | tr -d ' ",' | awk -v f="filename:$file" '$0==f{hit=1} hit && /^sha256:/{sub(/^sha256:/,""); print; exit}')
  fetch_verified "https://go.dev/dl/$file" "$want" "$tmp/go.tgz"
  rm -rf "$OPT/go" && tar -C "$OPT" -xzf "$tmp/go.tgz"
  ln -sf "$OPT/go/bin/go" "$BIN/go"; ln -sf "$OPT/go/bin/gofmt" "$BIN/gofmt"
  echo "Installed $ver in $OPT/go"
fi

if [ "$need_node" -eq 1 ]; then
  base="https://nodejs.org/dist/latest-v$NODE_LINE.x"
  line=$(curl -fsSL "$base/SHASUMS256.txt" | grep "$nodeos-$nodearch.tar.gz\$" | head -n 1)
  want=$(printf '%s' "$line" | awk '{print $1}'); file=$(printf '%s' "$line" | awk '{print $2}')
  [ -n "$file" ] || { echo "Could not find a Node.js $NODE_LINE build for $nodeos-$nodearch."; exit 1; }
  fetch_verified "$base/$file" "$want" "$tmp/node.tgz"
  rm -rf "$OPT/node" && mkdir -p "$OPT/node" && tar -C "$OPT/node" --strip-components=1 -xzf "$tmp/node.tgz"
  for b in node npm npx; do ln -sf "$OPT/node/bin/$b" "$BIN/$b"; done
  echo "Installed ${file%.tar.gz} in $OPT/node"
fi

echo
case ":$PATH:" in
  *":$BIN:"*) ;;
  *) echo "Add this to your shell profile (~/.zshrc or ~/.bashrc), then open a new terminal:"
     echo "  export PATH=\"$BIN:\$PATH\""; echo ;;
esac
PATH="$BIN:$PATH" sh "$here/check-prereqs.sh" all
