#!/bin/sh
# Checks that the tools needed to build Testplane are installed and recent
# enough. Usage: scripts/check-prereqs.sh [go|ui|all]   (default: all)
#   go  - only what "make build-go" needs (Go)
#   ui  - only what "make ui" needs (Node.js and npm)
want="${1:-all}"
GO_MIN_MAJOR=1; GO_MIN_MINOR=24
NODE_MIN_MAJOR=20
fail=0

ok()   { printf '  ok       %s\n' "$1"; }
bad()  { printf '  NEEDED   %s\n           %s\n' "$1" "$2"; fail=1; }

check_go() {
  if ! command -v go >/dev/null 2>&1; then
    bad "Go ${GO_MIN_MAJOR}.${GO_MIN_MINOR} or newer" "Install it from https://go.dev/dl/ and make sure 'go' is on your PATH."
    return
  fi
  v=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
  major=$(printf '%s' "$v" | cut -d. -f1)
  minor=$(printf '%s' "$v" | cut -d. -f2 | sed 's/[^0-9].*//')
  if [ -z "$major" ] || [ -z "$minor" ]; then
    bad "Go ${GO_MIN_MAJOR}.${GO_MIN_MINOR} or newer" "Could not read the version from 'go env GOVERSION' (got '$v')."
  elif [ "$major" -lt "$GO_MIN_MAJOR" ] || { [ "$major" -eq "$GO_MIN_MAJOR" ] && [ "$minor" -lt "$GO_MIN_MINOR" ]; }; then
    bad "Go ${GO_MIN_MAJOR}.${GO_MIN_MINOR} or newer (found $v)" "Upgrade from https://go.dev/dl/"
  else
    ok "Go $v"
  fi
}

check_node() {
  if ! command -v node >/dev/null 2>&1; then
    bad "Node.js ${NODE_MIN_MAJOR} or newer" "Install it from https://nodejs.org/ (only needed to rebuild the UI; 'make build-go' works without it)."
  else
    v=$(node -v 2>/dev/null | sed 's/^v//')
    major=$(printf '%s' "$v" | cut -d. -f1)
    if [ -z "$major" ] || ! [ "$major" -ge "$NODE_MIN_MAJOR" ] 2>/dev/null; then
      bad "Node.js ${NODE_MIN_MAJOR} or newer (found ${v:-unknown})" "Upgrade from https://nodejs.org/"
    else
      ok "Node.js $v"
    fi
  fi
  if ! command -v npm >/dev/null 2>&1; then
    bad "npm" "It ships with Node.js; reinstall Node.js from https://nodejs.org/"
  else
    ok "npm $(npm -v 2>/dev/null)"
  fi
}

echo "Checking prerequisites:"
case "$want" in
  go)  check_go ;;
  ui)  check_node ;;
  all) check_go; check_node ;;
  *)   echo "usage: $0 [go|ui|all]" >&2; exit 2 ;;
esac

if [ "$fail" -ne 0 ]; then
  echo
  echo "Some prerequisites are missing. See the Prerequisites section of README.md."
  exit 1
fi
