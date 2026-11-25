#!/usr/bin/env bash
set -eu

new=
if [[ ${1-} == --new ]]; then
	new=1
	shift
fi
[[ $# == 1 && $1 =~ ^[a-z0-9-]+$ ]] || { echo 'usage: holder.sh [--new] <role>' >&2; exit 2; }
dir=$(git rev-parse --absolute-git-dir)/maestro || exit 1
if [[ -n $new || ! -e $dir/holder ]]; then
	mkdir -p -- "$dir"
	draft=$(mktemp "$dir/holder.XXXXXXXX")
	printf '%s/%s\n' "$1" "$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')" >"$draft"
	if [[ -n $new ]]; then
		mv -f -- "$draft" "$dir/holder"
	else
		ln -- "$draft" "$dir/holder" 2>/dev/null || :
		rm -f -- "$draft"
	fi
fi
cat -- "$dir/holder"
