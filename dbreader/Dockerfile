# ------------------------------------------------------------
#  [*] dbreader image — two stages, ships one static binary
#
#  Stage 1 compiles in the full Go toolchain image — the Go
#  LND v0.21.4 is released with (its go.mod needs 1.26.8+);
#  stage 2 is a pinned Alpine with nothing but the binary.
#  CGO is off so the binary is fully static (the MySQL
#  driver and LND's bbolt store are pure Go) and runs on musl
#  without any libc; -s -w strips symbols and DWARF. Alpine
#  rather than scratch: it brings a shell for `docker exec`
#  when debugging. The image sets no user: compose runs it
#  as 1000:1000 with a read-only root filesystem and a tmpfs
#  on /tmp — the sync copies channel.db there, so /tmp must
#  be writable and large enough for the graph (0.5 GB and
#  growing). /data is the LND node's graph directory, owned
#  by 1000 like everything under _DATA, mounted read-only.
#
#  runTests.sh builds stage 1 alone (the "build" target)
#  and runs the tests in it: the same toolchain, the same
#  tidied dependencies and the same source — tests/ rides
#  along in app/ but never reaches stage 2.
# ------------------------------------------------------------




# ---- Stage 1: build --------------------------------------------
FROM golang:1.27.1 AS build

WORKDIR /src

# go.sum is not committed — tidy resolves and pins the
# dependency graph from the sources, then the build
# downloads whatever tidy listed
COPY app/ .
RUN go mod tidy
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lnd-dbreader .





# ---- Stage 2: run ----------------------------------------------
FROM alpine:3.24

WORKDIR /app

COPY --from=build /lnd-dbreader /app/lnd-dbreader

CMD ["./lnd-dbreader"]
