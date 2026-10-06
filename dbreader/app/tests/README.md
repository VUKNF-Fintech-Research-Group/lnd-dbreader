# dbreader regression tests

Three layers, from cheapest to heaviest:

| Layer | Files | Needs | When to run |
|---|---|---|---|
| JSON, graph reading, whole copies | `json_test.go`, `graph_test.go`, `snapshot_test.go` | nothing | always |
| Graph → MySQL | `import_test.go` | the test MySQL | always (via `runTests.sh`) |
| Service binary | `service_test.go` | the test MySQL, the binary | always (via `runTests.sh`) |

`../runTests.sh` (in `dbreader/`) runs all three: it builds the Dockerfile's
build stage — the toolchain, dependencies and source that ship — and runs
`go test` in it next to a throwaway MySQL 8.4.0 with the production
`my.cnf`, both on an internal network with no route out. Without
`DBREADER_TEST_MYSQL_HOST` the MySQL tests skip themselves, so the first
layer also runs on its own.

## The anchors

The crown jewel is `TestImportReproducesTheV0193GoldenRows`. It pins the
upgrade from LND v0.19.3 to v0.21.4 to data recorded **before** it:

- `testdata/channel-lnd-v0.19.3.db` — a small channel graph written by LND
  **v0.19.3's** own graph store (`testdata/fixturegen-v0.19.3/`, a module of
  its own pinned to v0.19.3; its header lists what the graph holds: every
  address type, an announced node with an empty alias, a never-announced
  shell node, channels with two, one and no policies, opaque TLV data
  longer than the 255-byte key prefix).
- `testdata/golden-v0.19.3.json` — every row the **production v0.19.3
  dbreader image** (`vuknf/lnd-dbreader-dbreader:20260826`) wrote for that
  file into MySQL 8.4.0.

The upgraded dbreader must reproduce those rows exactly — the unique keys
of the append-only tables are built from them, so any drift would add a
duplicate of every row on the first sync after deploying. The same graph
written by LND v0.21.4 (`writeFixtureGraph` in `helpers_test.go`) may
differ in one place only, the DNS hostname v0.19.3 could not decode
(`goldenForV0214`).

Never re-record the golden rows from the current code: they are the
pre-upgrade truth. A deliberate change of output becomes an explicit delta
in the test, like `goldenForV0214`, and is named in the commit message.

## Running

The full suite, from `dbreader/` (extra arguments go to `go test`):

    ./runTests.sh
    ./runTests.sh -v -run 'Import|Service'

Only the first layer, against the WORKING TREE, without building the image
(from `dbreader/`; the module cache volume keeps later runs fast):

    sudo docker run --rm -v ./app:/src:ro -v dbreader-gomod:/go/pkg/mod -e CGO_ENABLED=0 golang:1.27.1 \
      sh -c 'cp -r /src /b && cd /b && go mod tidy && go test -count=1 ./tests/...'

Re-recording the v0.19.3 fixture (only if the graph it holds must change —
then `writeFixtureGraph` changes with it, and the golden rows have to be
recorded again with the v0.19.3 image, which is a deliberate act; from
`dbreader/app/tests/testdata`):

    rm channel-lnd-v0.19.3.db
    sudo docker run --rm -v ./fixturegen-v0.19.3:/src:ro -v "$PWD":/out golang:1.23 \
      sh -c 'cp -r /src /b && cd /b && go mod tidy && go run . /out/channel-lnd-v0.19.3.db && chown 1000:1000 /out/channel-lnd-v0.19.3.db'

## Conventions

- Fixtures are opened through `models.OpenChannelGraph`, the production
  opener — never a test-only copy of it — and always on a private copy in
  the test's temp dir (`v0193Fixture`), so no test can change an anchor.
- Every MySQL test gets a database of its own (`newTestDatabase`), dropped
  when it ends; tables are created by `InitializeDatabaseTables`, as on
  every production sync.
- The service tests run one at a time: the binary always copies to
  `/tmp/channel_copy.db`.
- Style: test files are house files — a header banner per file and a
  banner per `TestX` saying what it pins down; the "Used by" of a test is
  the runner.
