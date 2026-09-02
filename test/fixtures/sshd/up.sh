#!/usr/bin/env bash
# Brings up one sshd container per distribution, and writes an ssh config
# that rove can discover them through. The probe is exercised end to end
# against real sshd on real distributions, never against anyone's servers.
#
#   ./up.sh
#   go test -tags=integration ./test/...
#   ./down.sh
#
# The generated key pair and config are local to this directory and ignored.
set -euo pipefail
cd "$(dirname "$0")"

# image|port|setup
#
# Creating the account is per-distro in two ways that both fail confusingly.
# Alpine has no usermod (busybox ships passwd instead), and every distro
# leaves a freshly created account *locked*: OpenSSH refuses a locked account
# even for key auth, and reports it as "Permission denied (publickey)", which
# looks exactly like a wrong key.
BOXES=(
  "alpine:3.20|22201|apk add --no-cache openssh && adduser -D -s /bin/sh tester && passwd -u tester"
  "ubuntu:24.04|22202|apt-get update && apt-get install -y --no-install-recommends openssh-server && rm -rf /var/lib/apt/lists/* && useradd -m -s /bin/sh tester && usermod -p '*' tester"
  "debian:12|22203|apt-get update && apt-get install -y --no-install-recommends openssh-server && rm -rf /var/lib/apt/lists/* && useradd -m -s /bin/sh tester && usermod -p '*' tester"
  "rockylinux:9|22204|dnf install -y --setopt=install_weak_deps=False openssh-server iproute procps-ng && dnf clean all && useradd -m -s /bin/sh tester && usermod -p '*' tester"
  "amazonlinux:2023|22205|dnf install -y --setopt=install_weak_deps=False openssh-server shadow-utils procps-ng iproute && dnf clean all && useradd -m -s /bin/sh tester && usermod -p '*' tester"
)

[ -f tk ] || ssh-keygen -t ed25519 -N '' -C 'rove-sshd-fixture' -f tk -q
cp tk.pub authorized_keys

: > config
for box in "${BOXES[@]}"; do
  IFS='|' read -r image port install <<< "$box"
  slug=$(echo "$image" | tr ':.' '--')
  name="rove-fixture-$slug"

  cat > "Dockerfile.$slug" <<DOCKEREOF
FROM $image
RUN $install \\
 && ssh-keygen -A \\
 && mkdir -p /home/tester/.ssh /run/sshd /var/run/sshd
COPY authorized_keys /home/tester/.ssh/authorized_keys
RUN chown -R tester /home/tester/.ssh \\
 && chmod 700 /home/tester/.ssh \\
 && chmod 600 /home/tester/.ssh/authorized_keys
EXPOSE 22
CMD ["/usr/sbin/sshd","-D","-e"]
DOCKEREOF

  echo "building $image ..."
  docker build -q -f "Dockerfile.$slug" -t "$name" . >/dev/null
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker run -d --name "$name" -p "$port:22" "$name" >/dev/null

  # Throwaway host keys are regenerated on every build, so this fixture
  # opts out of known_hosts. Nothing in rove itself ever does this.
  cat >> config <<CONFIGEOF
# rove: env=fixture tags=$slug
Host $name
    HostName 127.0.0.1
    Port $port
    User tester
    IdentityFile $(pwd)/tk
    IdentitiesOnly yes
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    LogLevel ERROR

CONFIGEOF
done

# systemd needs a real init as PID 1, which the plain boxes above cannot
# give us. It gets its own image and run flags, plus a unit that always
# fails: without one, nothing in rove's failed-unit handling is ever
# exercised against a real systemd.
SYSTEMD_PORT=22206
SYSTEMD_NAME=rove-fixture-ubuntu-systemd
cat > Dockerfile.ubuntu-systemd <<'DOCKEREOF'
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update  && apt-get install -y --no-install-recommends systemd systemd-sysv dbus openssh-server iproute2 procps  && rm -rf /var/lib/apt/lists/*  && ssh-keygen -A  && useradd -m -s /bin/sh tester  && usermod -p '*' tester  && mkdir -p /home/tester/.ssh  && systemctl enable ssh dbus
COPY authorized_keys /home/tester/.ssh/authorized_keys
RUN chown -R tester /home/tester/.ssh  && chmod 700 /home/tester/.ssh  && chmod 600 /home/tester/.ssh/authorized_keys
RUN printf '[Unit]\nDescription=Deliberately failing fixture unit\n[Service]\nType=oneshot\nExecStart=/bin/false\n[Install]\nWantedBy=multi-user.target\n' \
      > /etc/systemd/system/rove-broken.service \
 && systemctl enable rove-broken.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
DOCKEREOF

echo "building ubuntu:24.04 with systemd ..."
docker build -q -f Dockerfile.ubuntu-systemd -t "$SYSTEMD_NAME" . >/dev/null
docker rm -f "$SYSTEMD_NAME" >/dev/null 2>&1 || true
docker run -d --name "$SYSTEMD_NAME"   --privileged --cgroupns=host   -v /sys/fs/cgroup:/sys/fs/cgroup:rw   -p "$SYSTEMD_PORT:22" "$SYSTEMD_NAME" >/dev/null

cat >> config <<CONFIGEOF
# rove: env=fixture tags=ubuntu-systemd,systemd
Host $SYSTEMD_NAME
    HostName 127.0.0.1
    Port $SYSTEMD_PORT
    User tester
    IdentityFile $(pwd)/tk
    IdentitiesOnly yes
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    LogLevel ERROR

CONFIGEOF

for box in "${BOXES[@]}"; do
  IFS='|' read -r image port install <<< "$box"
  printf 'waiting for %s on %s ' "$image" "$port"
  for _ in $(seq 1 60); do
    if nc -z 127.0.0.1 "$port" 2>/dev/null; then break; fi
    sleep 1
  done
  echo "ok"
done

printf 'waiting for systemd box on %s ' "$SYSTEMD_PORT"
for _ in $(seq 1 90); do
  if nc -z 127.0.0.1 "$SYSTEMD_PORT" 2>/dev/null; then break; fi
  sleep 1
done
echo "ok"

# The same hosts with no usable key, so the auth-failure path can be tested
# against a real sshd rejecting a real connection.
sed 's|IdentityFile .*|IdentityFile /dev/null|' config > config-nokey

echo
echo "config written to $(pwd)/config"
