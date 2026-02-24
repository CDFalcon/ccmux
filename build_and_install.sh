#!/bin/bash
set -e

cd "$(dirname "$0")"

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS="-X github.com/CDFalcon/ccmux/internal/version.Version=${VERSION}"
LDFLAGS="${LDFLAGS} -X github.com/CDFalcon/ccmux/internal/version.GitCommit=${GIT_COMMIT}"
LDFLAGS="${LDFLAGS} -X github.com/CDFalcon/ccmux/internal/version.BuildDate=${BUILD_DATE}"

go build -ldflags "${LDFLAGS}" -o ccmux ./cmd/ccmux
rm -f ~/bin/ccmux
mv ccmux ~/bin/ccmux
echo "Installed ccmux ${VERSION} to ~/bin/ccmux"

for session in $(tmux list-sessions -F '#S' 2>/dev/null | grep '^ccmux-'); do
	tmux kill-session -t "$session"
	echo "Killed session: $session"
done
