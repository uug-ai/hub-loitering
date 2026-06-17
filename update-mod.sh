#!/usr/bin/env bash
set -euo pipefail

# Regenerate go.mod/go.sum for the hub-loitering module from scratch. Run from
# the module root. GOSUMDB is disabled because the uug-ai modules resolve from
# the workspace (go.work), not the public checksum database.
export GOSUMDB=off

rm -f go.mod go.sum
go mod init github.com/uug-ai/hub-loitering
go mod tidy
