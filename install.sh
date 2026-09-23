#!/bin/sh
set -eu

fail() {
    printf 'mux: %s\n' "$*" >&2
    exit 1
}

for tool in go git tmux; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required. Install it and run this script again."
done

# Match the minimum toolchain in go.mod. Go handles newer requirements
# when installing a future mux version.
go_version=$(go env GOVERSION)
go_number=${go_version#go}
if ! printf '%s\n' "$go_number" | awk -F. '
    /^[0-9]+\.[0-9]+/ {
        if ($1 > 1 || ($1 == 1 && ($2 > 25 || ($2 == 25 && $3 + 0 >= 8)))) exit 0
    }
    { exit 1 }
'; then
    fail "Go 1.25.8 or newer is required (found $go_version)."
fi

tmux_version=$(tmux -V)
if ! printf '%s\n' "$tmux_version" | awk '
    $1 == "tmux" {
        split($2, v, ".")
        if (v[1] + 0 > 3 || (v[1] + 0 == 3 && v[2] + 0 >= 2)) exit 0
    }
    { exit 1 }
'; then
    fail "tmux 3.2 or newer is required (found $tmux_version)."
fi

install_dir=${MUX_INSTALL_DIR:-"$HOME/.local/bin"}
version=${MUX_VERSION:-latest}
case "$install_dir" in
    /*) ;;
    *) fail 'MUX_INSTALL_DIR must be an absolute path.' ;;
esac

mkdir -p "$install_dir"
printf 'Building mux (%s)...\n' "$version"
GOBIN="$install_dir" go install "github.com/Amansingh-afk/mux/cmd/mux@$version"
printf 'Installed mux to %s/mux\n' "$install_dir"
case ":$PATH:" in
    *":$install_dir:"*) printf 'Run mux to start.\n' ;;
    *)
        printf 'Add this directory to PATH in your shell config: %s\n' "$install_dir"
        printf 'Then open a new terminal and run mux.\n'
        ;;
esac
