#!/usr/bin/env bash
set -euo pipefail

evidence="${1:?native runtime evidence directory required}"
for inventory in apk-file-owners.tsv native-files.txt native-needed.txt; do
  if [ ! -s "$evidence/$inventory" ]; then
    echo "Empty native ownership inventory: $inventory" >&2
    exit 1
  fi
done

awk '
  FNR == NR { owner[$1] = $0; next }
  !($0 in owner) { print "No installed APK owns " $0 > "/dev/stderr"; missing = 1; next }
  { print owner[$0] }
  END { exit missing }
' "$evidence/apk-file-owners.tsv" "$evidence/native-files.txt" \
  > "$evidence/native-file-owners.tsv"

awk '
  FNR == NR {
    count = split($1, path, "/")
    owner[path[count]] = $0
    next
  }
  !($0 in owner) { print "No installed APK owns required library " $0 > "/dev/stderr"; missing = 1; next }
  { print owner[$0] }
  END { exit missing }
' "$evidence/apk-file-owners.tsv" "$evidence/native-needed.txt" \
  | tee "$evidence/native-dependency-owners.tsv"
