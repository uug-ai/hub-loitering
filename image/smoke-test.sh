#!/usr/bin/env bash
# Additional worker smoke for the RabbitMQ-only static image: the binary is
# fully static (no interpreter, no shared libraries), the runtime ships no
# native libraries such as librdkafka, and the CA bundle and a sticky,
# world-writable /tmp are present.
set -euo pipefail

image="${1:?image reference required}"
evidence="${RUNTIME_EVIDENCE_DIR:-runtime-evidence}"
mkdir -p "$evidence"
container="$(docker create "$image")"
trap 'docker rm "$container" > /dev/null' EXIT

docker export "$container" -o "$evidence/rootfs.tar"
tar -tvf "$evidence/rootfs.tar" > "$evidence/rootfs-listing.txt"
docker cp "$container:/app/main" "$evidence/main"

readelf --program-headers "$evidence/main" > "$evidence/program-headers.txt"
if grep -q 'INTERP' "$evidence/program-headers.txt"; then
  echo "/app/main requests a program interpreter; expected a static binary" >&2
  exit 1
fi
if readelf --dynamic "$evidence/main" | grep -q 'NEEDED'; then
  echo "/app/main links shared libraries; expected a static binary" >&2
  exit 1
fi

if grep -E '\.so(\.[0-9]+)*$' "$evidence/rootfs-listing.txt"; then
  echo "runtime ships shared libraries; expected none in the static image" >&2
  exit 1
fi
if grep -i 'rdkafka' "$evidence/rootfs-listing.txt"; then
  echo "runtime ships librdkafka; the image is RabbitMQ-only" >&2
  exit 1
fi

grep -Eq ' (\./)?etc/ssl/certs/ca-certificates\.crt$' "$evidence/rootfs-listing.txt"
grep -Eq '^drwxrwxrwt .* (\./)?tmp/?$' "$evidence/rootfs-listing.txt"

rm -f "$evidence/rootfs.tar"
echo "Static RabbitMQ-only runtime contract verified"
