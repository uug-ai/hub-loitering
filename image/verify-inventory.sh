#!/usr/bin/env bash
set -euo pipefail

evidence="${1:?native image evidence directory required}"
runtime="$evidence/runtime-evidence"
awk '
  /^P:/ { package = substr($0, 3) }
  /^V:/ { print package "\t" substr($0, 3) }
' "$runtime/apk-installed" | sort -u > "$evidence/closure-apk.tsv"
jq -r '.Results[]? | select(.Class == "os-pkgs") | .Packages[]? |
  [.Name, .Version] | @tsv' "$evidence/image-security.json" \
  | sort -u > "$evidence/closure-scanner.tsv"
jq -r '.packages[]? |
  select(any(.externalRefs[]?; .referenceType == "purl" and (.referenceLocator | startswith("pkg:apk/")))) |
  [.name, .versionInfo] | @tsv' "$evidence/image.spdx.json" \
  | sort -u > "$evidence/closure-spdx.tsv"

test -s "$evidence/closure-apk.tsv"
diff -u "$evidence/closure-apk.tsv" "$evidence/closure-scanner.tsv"
diff -u "$evidence/closure-apk.tsv" "$evidence/closure-spdx.tsv"
jq -e '(.Metadata.OS.Family == "wolfi") and (.Metadata.OS.EOSL != true) and
  ([.Results[]? | (.Vulnerabilities[]?, .Secrets[]?) |
    select(.Severity == "MEDIUM" or .Severity == "HIGH" or .Severity == "CRITICAL")] | length == 0)
' "$evidence/image-security.json" > /dev/null
test -s "$runtime/native-file-owners.tsv"
test -s "$runtime/native-dependency-owners.tsv"
grep -q $'\tlibrdkafka\t2.16.0-r0$' "$runtime/native-dependency-owners.tsv"
grep -q -- '--- PASS: TestDynamicKafkaNativeRuntime' "$runtime/kafka-runtime.log"
{
  printf 'APK packages: %s\n' "$(wc -l < "$evidence/closure-apk.tsv")"
  printf 'Scanner OS packages: %s\n' "$(wc -l < "$evidence/closure-scanner.tsv")"
  printf 'SPDX APK packages: %s\n' "$(wc -l < "$evidence/closure-spdx.tsv")"
  printf 'Package-owned native files: %s\n' "$(wc -l < "$runtime/native-file-owners.tsv")"
  printf 'Package-owned ELF dependencies: %s\n' "$(wc -l < "$runtime/native-dependency-owners.tsv")"
} | tee "$evidence/closure-summary.txt"
