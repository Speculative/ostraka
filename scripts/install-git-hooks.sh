#!/bin/sh
set -eu

repo_root=$(git rev-parse --show-toplevel)
existing_hooks_path=$(git -C "$repo_root" config --local --get core.hooksPath || true)
if [ -n "$existing_hooks_path" ] && [ "$existing_hooks_path" != ".githooks" ]; then
	printf 'Refusing to replace existing core.hooksPath: %s\n' "$existing_hooks_path" >&2
	exit 1
fi

git -C "$repo_root" config --local core.hooksPath .githooks
printf 'Installed commit hooks from %s/.githooks\n' "$repo_root"
