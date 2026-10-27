#!/usr/bin/env bash
set -euo pipefail

image="${1:?image reference required}"
evidence="${RUNTIME_EVIDENCE_DIR:-runtime-evidence}"
mkdir -p "$evidence"
container="$(docker create "$image")"
test_container=
test_image="loitering-kafka-test:${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}"
cleanup() {
  docker rm "$container" > /dev/null
  if [ -n "$test_container" ]; then
    docker rm "$test_container" > /dev/null
  fi
}
trap cleanup EXIT

docker cp "$container:/lib/apk/db/installed" "$evidence/apk-installed"
docker cp "$container:/usr/lib" "$evidence/native-libraries"
docker cp "$container:/app/main" "$evidence/main"

readelf --dynamic "$evidence/main" > "$evidence/native-elf.txt"
readelf --program-headers "$evidence/main" > "$evidence/program-headers.txt"
grep -q 'Shared library: \[librdkafka.so.1\]' "$evidence/native-elf.txt"
: > "$evidence/native-non-elf.txt"
: > "$evidence/native-files.txt"
while IFS= read -r library; do
  printf '/usr/lib/%s\n' "${library#"$evidence/native-libraries/"}" >> "$evidence/native-files.txt"
  magic="$(od -An -tx1 -N4 "$library" | tr -d '[:space:]')"
  if [ "$magic" = 7f454c46 ]; then
    readelf --dynamic "$library" >> "$evidence/native-elf.txt"
  else
    sha256sum "$library" >> "$evidence/native-non-elf.txt"
  fi
done < <(find "$evidence/native-libraries" -type f -name '*.so*' | sort)

awk '
  /^P:/ { package = substr($0, 3) }
  /^V:/ { version = substr($0, 3) }
  /^F:/ { directory = substr($0, 3) }
  /^R:/ { print "/" directory "/" substr($0, 3) "\t" package "\t" version }
' "$evidence/apk-installed" > "$evidence/apk-file-owners.tsv"

awk '/NEEDED/ {
  sub(/^.*\[/, "")
  sub(/\].*$/, "")
  print
}' "$evidence/native-elf.txt" | sort -u > "$evidence/native-needed.txt"
awk '/Requesting program interpreter:/ {
  sub(/^.*: /, "")
  sub(/\].*$/, "")
  sub(/^.*\//, "")
  print
}' "$evidence/program-headers.txt" >> "$evidence/native-needed.txt"
sort -u -o "$evidence/native-needed.txt" "$evidence/native-needed.txt"

bash image/verify-native-owners.sh "$evidence"

build_args=(--target test --load --tag "$test_image" --file "${DOCKERFILE:-Dockerfile}")
if [ -n "${GH_TOKEN:-}" ]; then
  build_args+=(--secret id=github_token,env=GH_TOKEN)
fi
docker buildx build "${build_args[@]}" .
test_container="$(docker create "$test_image")"
docker cp "$test_container:/out/kafka.test" "$evidence/kafka.test"
chmod 0555 "$evidence/kafka.test"
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 65532:65532 \
  --mount "type=bind,src=$(realpath "$evidence/kafka.test"),dst=/kafka.test,readonly" \
  -e LOITERING_EXACT_RUNTIME=1 --entrypoint /kafka.test "$image" \
  -test.run '^TestDynamicKafkaNativeRuntime$' -test.timeout 30s -test.v \
  2>&1 | tee "$evidence/kafka-runtime.log"
