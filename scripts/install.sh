#!/bin/sh
set -eu

fail() { printf 'Installation failed: %s\n' "$*" >&2; exit 1; }
directory=${XDG_DATA_HOME:-"$HOME/.local/share"}/vgalary
update=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --directory)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail '--directory requires a path'
            directory=$2
            shift 2
            ;;
        --update) update=true; shift ;;
        --help|-h)
            printf '%s\n' 'Usage: sh install.sh [--directory PATH] [--update]' \
                'Requires Git, Node.js 22.13+, npm and Go 1.27.3+.' \
                'Builds and installs locally. Does not start a server or modify PATH.'
            exit 0
            ;;
        *) fail "Unknown option: $1" ;;
    esac
done

for tool in git node npm go; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool must be installed and on PATH"
done
node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a<22||(a===22&&b<13)?1:0)' \
    || fail 'Node.js 22.13+ is required'

case "$directory" in /*) ;; *) directory=$PWD/$directory ;; esac
mkdir -p "$directory"
directory=$(CDPATH= cd -P "$directory" && pwd)
source=$directory/source
repository=https://github.com/snowmerak/vgalary.git
GIT_TERMINAL_PROMPT=0
export GIT_TERMINAL_PROMPT
if [ -e "$source" ]; then
    [ -e "$source/.git" ] || fail 'Existing source is not a Git checkout; choose another --directory'
    prefix=$(git -C "$source" rev-parse --show-prefix)
    [ -z "$prefix" ] || fail 'Existing source must be its own Git checkout'
    origin=$(git -C "$source" config --get remote.origin.url)
    [ "$origin" = "$repository" ] || fail 'Existing checkout has a different repository'
    changes=$(git -C "$source" status --porcelain)
    [ -z "$changes" ] || fail 'Existing checkout contains local changes; preserve them before updating'
    if [ "$update" = true ]; then git -C "$source" pull --ff-only; fi
else
    git clone --depth 1 -- "$repository" "$source"
fi
[ -f "$source/scripts/deploy.mjs" ] || fail 'Checkout has no scripts/deploy.mjs'
node "$source/scripts/deploy.mjs" --build-only
[ -f "$source/.deploy/vgalary" ] || fail 'Build did not produce .deploy/vgalary'

mkdir -p "$directory/bin"
[ ! -d "$directory/bin/vgalary" ] || fail 'Destination executable is a directory'
temporary=$(mktemp "$directory/bin/.vgalary.XXXXXX")
cleanup() {
    result=$?
    trap - 0
    if ! rm -f "$temporary"; then
        printf 'Could not remove temporary file: %s\n' "$temporary" >&2
        [ "$result" -ne 0 ] || result=1
    fi
    exit "$result"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM
cp "$source/.deploy/vgalary" "$temporary"
chmod 755 "$temporary"
mv -f "$temporary" "$directory/bin/vgalary"
printf '\nInstalled executable: %s\n' "$directory/bin/vgalary"
printf '%s\n' 'Run this executable, then open http://127.0.0.1:8181' \
    'Indexing additionally needs ffmpeg/ffprobe, llama.cpp and Qdrant.'
