#!/bin/sh
set -eu

uid="${PUID:-1000}"
gid="${PGID:-1000}"

case "$uid:$gid" in
  *[!0-9:]* | :* | *: | *::* )
    echo "PUID and PGID must be numeric" >&2
    exit 2
    ;;
esac

if id dev >/dev/null 2>&1; then
  current_uid="$(id -u dev)"
  current_gid="$(id -g dev)"
  if [ "$current_uid" != "$uid" ] || [ "$current_gid" != "$gid" ]; then
    deluser dev
  fi
fi

group="$(awk -F: -v gid="$gid" '$3 == gid { print $1; exit }' /etc/group)"
if [ -z "$group" ]; then
  group=dev
  if ! getent group dev >/dev/null 2>&1; then
    addgroup -S -g "$gid" dev
  else
    current_gid="$(getent group dev | cut -d: -f3)"
    if [ "$current_gid" != "$gid" ]; then
      delgroup dev
      addgroup -S -g "$gid" dev
    fi
  fi
elif [ "$group" = "dev" ]; then
  :
else
  if getent group dev >/dev/null 2>&1; then
    delgroup dev
  fi
fi

if ! id dev >/dev/null 2>&1; then
  adduser -S -D -H -u "$uid" -G "$group" -s /bin/bash dev
fi

mkdir -p \
  /home/dev/.cache \
  /home/dev/.config \
  /home/dev/.local/bin \
  /home/dev/.local/share \
  /home/dev/go/bin \
  /state
chown -R dev:"$group" /home/dev /state

exec su-exec dev:"$group" "$@"
