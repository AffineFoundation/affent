#!/bin/sh
set -eu

. /usr/local/bin/affent-go-cgroup-env

exec "$@"
