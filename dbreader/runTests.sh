#!/bin/sh
# -----------------------------------------------------------
#  [*] dbreader — regression test runner
#
#  Builds the dbreader image's BUILD stage from this folder
#  under a tag of its own — the same Go toolchain, the same
#  tidied LND v0.21.4 dependencies and the same source that
#  ship; the running dbreader's image is left alone — and
#  runs the suite in app/tests inside a throwaway container,
#  next to a throwaway MySQL 8.4.0 that carries the
#  production my.cnf and keeps its data on a tmpfs. Both sit
#  on an internal network of their own: there is no route
#  out, so a test that reaches out fails instead of passing
#  by luck, and the live stack's MySQL is out of reach. The
#  service tests run /lnd-dbreader, the very binary the
#  production image copies; the compile settings match the
#  image build, so its package cache is reused. Containers
#  and network are removed on exit, after a failure or
#  Ctrl-C as well. Extra arguments go to go test — a
#  test-name filter, verbose output.
#
#  tests/README.md has the layers, the story of the anchors
#  and the quicker loops while developing.
# -----------------------------------------------------------
set -e
cd "$(dirname "$0")"

NAME=lnd-dbreader-tests

cleanup() {
  sudo docker rm -f "$NAME-mysql" >/dev/null 2>&1 || true
  sudo docker network rm "$NAME" >/dev/null 2>&1 || true
}

# A run killed halfway leaves its MySQL behind — clear it
# first; the EXIT trap also fires through the INT/TERM exit
cleanup
trap cleanup EXIT
trap 'exit 130' INT TERM

sudo docker build --target build -t lnd-dbreader-dbreader-check .
sudo docker network create --internal "$NAME" >/dev/null
sudo docker run -d --name "$NAME-mysql" --network "$NAME" \
  -e MYSQL_ROOT_PASSWORD=root --tmpfs /var/lib/mysql \
  -v "$(cd .. && pwd)/mysql/my.cnf:/etc/mysql/conf.d/my.cnf:ro" \
  mysql:8.4.0 >/dev/null
sudo docker run --rm --name "$NAME" --network "$NAME" \
  -e DBREADER_TEST_MYSQL_HOST="$NAME-mysql" -e DBREADER_BIN=/lnd-dbreader \
  -e CGO_ENABLED=0 -e GOFLAGS=-trimpath \
  lnd-dbreader-dbreader-check go test -count=1 ./tests/... "$@"
