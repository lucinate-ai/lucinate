# Go toolchain environment for this host. Source it, do not execute it:
#
#   . scripts/lucinate-go-env.sh
#
# Go is installed on E: (C: runs at ~99% full) and so are the module and
# build caches — a default GOPATH on C: fills the disk and fails builds
# with "no space left on device". Every entry point (scripts/ltest,
# scripts/lbuild, the Makefile) sources this so they agree.

if ! command -v go >/dev/null 2>&1; then
  export PATH="/e/tools/go/bin:$PATH"
fi

# Only set these when the caller has not, so a properly configured shell
# keeps its own values.
: "${GOPATH:=E:/tools/gopath}"
: "${GOMODCACHE:=E:/tools/gopath/pkg/mod}"
: "${GOCACHE:=E:/tools/gocache}"
export GOPATH GOMODCACHE GOCACHE

mkdir -p "$GOPATH" "$GOCACHE" 2>/dev/null || true
