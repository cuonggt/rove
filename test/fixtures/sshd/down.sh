#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
docker ps -aq --filter 'name=rove-fixture-' | xargs -r docker rm -f >/dev/null
rm -f config config-nokey authorized_keys Dockerfile.*
echo "fixtures removed"
