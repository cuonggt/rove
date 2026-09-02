#!/usr/bin/env bash
# Writes an ssh config that points plausible server names at the local sshd
# fixtures, so the demo recording shows what rove looks like in use without
# putting anybody's real infrastructure in a gif.
#
#   test/fixtures/sshd/up.sh
#   docs/demo-setup.sh
#   vhs docs/demo.tape
set -euo pipefail
cd "$(dirname "$0")/.."

KEY="$(pwd)/test/fixtures/sshd/tk"
[ -f "$KEY" ] || { echo "run test/fixtures/sshd/up.sh first" >&2; exit 1; }

# alias|port|env|tags
HOSTS=(
  "prod-api|22206|prod|web"
  "prod-worker|22202|prod|worker"
  "prod-db|22204|prod|db"
  "staging-api|22203|staging|web"
  "edge-01|22201|prod|edge"
  "home-server|22205|personal|nas"
)

: > docs/demo.config
for h in "${HOSTS[@]}"; do
  IFS='|' read -r alias port env tags <<< "$h"
  cat >> docs/demo.config <<CFG
# rove: env=$env tags=$tags
Host $alias
    HostName 127.0.0.1
    Port $port
    User tester
    IdentityFile $KEY
    IdentitiesOnly yes
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    LogLevel ERROR

CFG
done
echo "wrote docs/demo.config"
