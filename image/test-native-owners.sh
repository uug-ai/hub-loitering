#!/usr/bin/env bash
set -euo pipefail

source="${1:?genuine native runtime evidence directory required}"
fixtures="${2:?new test fixture directory required}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir "$fixtures"
for fixture in positive empty missing; do
  mkdir "$fixtures/$fixture"
  cp "$source/apk-file-owners.tsv" "$source/native-files.txt" "$source/native-needed.txt" "$fixtures/$fixture/"
done

bash "$script_dir/verify-native-owners.sh" "$fixtures/positive" > "$fixtures/positive.log"

: > "$fixtures/empty/apk-file-owners.tsv"
if bash "$script_dir/verify-native-owners.sh" "$fixtures/empty" > "$fixtures/empty.log" 2>&1; then
  echo "Empty-owner negative verification unexpectedly passed" >&2
  exit 1
fi
grep -Fx 'Empty native ownership inventory: apk-file-owners.tsv' "$fixtures/empty.log"

awk '$2 != "librdkafka"' "$source/apk-file-owners.tsv" > "$fixtures/missing/apk-file-owners.tsv"
if bash "$script_dir/verify-native-owners.sh" "$fixtures/missing" > "$fixtures/missing.log" 2>&1; then
  echo "Missing-owner negative verification unexpectedly passed" >&2
  exit 1
fi
grep -F 'No installed APK owns /usr/lib/librdkafka' "$fixtures/missing.log"
echo "Genuine ownership positive, empty-owner negative and missing-owner negative verification passed"
