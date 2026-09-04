#!/bin/bash
# Saves the clipboard image, if any, to a file and prints its path.
# Exits 1 when the clipboard holds no image so the caller can paste text.
set -o pipefail
types=$(wl-paste --list-types 2>/dev/null) || exit 1
mime=$(grep -m1 '^image/' <<<"$types") || exit 1
ext=${mime#image/}; [[ $ext == jpeg ]] && ext=jpg
dir="${XDG_RUNTIME_DIR:-/tmp}/shedit/paste"
mkdir -p "$dir"
file="$dir/$(date +%Y%m%d-%H%M%S)-$RANDOM.$ext"
wl-paste --type "$mime" >"$file" || { rm -f "$file"; exit 1; }
[[ -s $file ]] || { rm -f "$file"; exit 1; }
printf '%s\n' "$file"
