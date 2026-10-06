// -----------------------------------------------------------
//  [*] tests — importers vs the anchors (import_test.go)
//
//  The importers in db/ end to end on the throwaway MySQL
//  (8.4.0 with the production my.cnf, as runTests.sh starts
//  it). The crown jewel is the first test: the v0.19.3
//  fixture must reproduce the golden rows the v0.19.3
//  dbreader wrote EXACTLY — the unique keys are built from
//  those values, so any drift after the upgrade would add a
//  duplicate of every row to the append-only history. Then
//  the upgrade itself (only the DNS hostname may change),
//  the history semantics — re-imports only move last_seen,
//  renames add rows, batch boundaries lose nothing — and
//  the longest address the gossip protocol allows.
// -----------------------------------------------------------


package tests

import (
	// Standard library
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	// LND
	lndmodels "github.com/lightningnetwork/lnd/graph/db/models"
	"github.com/lightningnetwork/lnd/lnwire"
)








// A node_announcement body is at most lnwire.MaxMsgBody
// bytes; 140 go to the signature, the empty feature
// vector's length, timestamp, node id, colour, alias and
// the address list's own length — the rest may be addresses
const maxAddressListBytes = lnwire.MaxMsgBody - 140








// -----------------------------------------------------------
// TestImportReproducesTheV0193GoldenRows
// -----------------------------------------------------------
//
// The anchor: the graph LND v0.19.3 stored, imported by
// this dbreader, gives column for column the rows the
// production v0.19.3 dbreader image wrote for the same file
// — node, address and channel keys, aliases (multibyte,
// empty), colours (#000000 for the shell node), opaque data
// whose hex outgrows the 255-character key prefix, and
// every json_data.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestImportReproducesTheV0193GoldenRows(t *testing.T) {
	conn, _, _ := newTestDatabase(t)

	runImport(t, openGraph(t, v0193Fixture(t)), conn)

	assertRowsEqual(t, loadGolden(t), dumpRows(t, conn))
}








// -----------------------------------------------------------
// TestImportOfAV0214GraphDiffersOnlyInTheDNSHostname
// -----------------------------------------------------------
//
// The same graph written by LND v0.21.4 — what the node
// stores after the upgrade — imports completely (the
// v0.19.3 dbreader failed the whole node walk here) and
// gives the golden rows with exactly one change: bravo's
// hostname is a real address row (fixtureHostname, port
// 9735) and "tcp" in its JSON.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestImportOfAV0214GraphDiffersOnlyInTheDNSHostname(t *testing.T) {
	conn, _, _ := newTestDatabase(t)

	runImport(t, openGraph(t, v0214Fixture(t)), conn)

	assertRowsEqual(t, goldenForV0214(t), dumpRows(t, conn))
}








// -----------------------------------------------------------
// TestUpgradeKeepsEveryRowAndAddsTheHostname
// -----------------------------------------------------------
//
// The day of the upgrade, replayed: a database filled from
// the v0.19.3-written graph, then synced from the
// v0.21.4-written one. Every unique key holds — still 3
// channels and 4 nodes, no duplicates — and node_addresses
// gains exactly the hostname row while the old opaque row
// stays behind as history.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestUpgradeKeepsEveryRowAndAddsTheHostname(t *testing.T) {
	conn, _, _ := newTestDatabase(t)

	runImport(t, openGraph(t, v0193Fixture(t)), conn)
	runImport(t, openGraph(t, v0214Fixture(t)), conn)

	if n := countRows(t, conn, "channel_announcements"); n != 3 {
		t.Errorf("channel_announcements = %d rows, want 3", n)
	}
	if n := countRows(t, conn, "node_announcements"); n != 4 {
		t.Errorf("node_announcements = %d rows, want 4", n)
	}
	if n := countRows(t, conn, "node_addresses"); n != 8 {
		t.Errorf("node_addresses = %d rows, want 8 (7 + the hostname)", n)
	}

	for _, address := range []string{fixtureOpaqueDNS, fixtureHostname} {
		var n int
		if err := conn.QueryRow("SELECT COUNT(*) FROM node_addresses WHERE address = ?", address).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", address, err)
		}
		if n != 1 {
			t.Errorf("rows for %s = %d, want 1", address, n)
		}
	}
}








// -----------------------------------------------------------
// TestReimportOnlyMovesLastSeen
// -----------------------------------------------------------
//
// Every sync re-imports the whole graph: the second import
// must hit every unique key — no new ids, first_seen kept —
// and bump last_seen past first_seen on every row of all
// three tables. TIMESTAMP has whole seconds, hence the
// pause.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestReimportOnlyMovesLastSeen(t *testing.T) {
	conn, _, _ := newTestDatabase(t)
	graph := openGraph(t, v0193Fixture(t))
	tables := []string{"channel_announcements", "node_announcements", "node_addresses"}

	firstSeen := func(table string) string {
		var digest sql.NullString
		query := fmt.Sprintf("SELECT GROUP_CONCAT(id, '@', first_seen ORDER BY id) FROM %s", table)
		if err := conn.QueryRow(query).Scan(&digest); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		return digest.String
	}

	runImport(t, graph, conn)
	before := map[string]string{}
	for _, table := range tables {
		before[table] = firstSeen(table)
	}

	time.Sleep(1100 * time.Millisecond)
	runImport(t, graph, conn)

	for _, table := range tables {
		if after := firstSeen(table); after != before[table] {
			t.Errorf("%s ids/first_seen changed:\nbefore %s\n after %s", table, before[table], after)
		}

		var stale int
		if err := conn.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE last_seen <= first_seen").Scan(&stale); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		if stale != 0 {
			t.Errorf("%s: %d rows kept last_seen at first_seen after the re-import", table, stale)
		}
	}
}








// -----------------------------------------------------------
// TestRenamedNodeAddsAHistoryRow
// -----------------------------------------------------------
//
// unique_node is (node_id, alias, rgb_color): a node that
// renames itself gets a NEW row and the old name stays as
// history; its unchanged addresses do not multiply.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestRenamedNodeAddsAHistoryRow(t *testing.T) {
	conn, _, _ := newTestDatabase(t)

	runImport(t, &fakeGraph{nodes: []*lndmodels.Node{syntheticNode(1, "before")}}, conn)
	runImport(t, &fakeGraph{nodes: []*lndmodels.Node{syntheticNode(1, "after")}}, conn)

	rows, err := conn.Query("SELECT alias FROM node_announcements ORDER BY id")
	if err != nil {
		t.Fatalf("read aliases: %v", err)
	}
	defer rows.Close()

	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			t.Fatalf("scan: %v", err)
		}
		aliases = append(aliases, alias)
	}
	if len(aliases) != 2 || aliases[0] != "before" || aliases[1] != "after" {
		t.Errorf("aliases = %v, want [before after]", aliases)
	}

	if n := countRows(t, conn, "node_addresses"); n != 2 {
		t.Errorf("node_addresses = %d rows, want 2", n)
	}
}








// -----------------------------------------------------------
// TestBatchBoundariesLoseNothing
// -----------------------------------------------------------
//
// Rows go out in INSERT statements of 5000: 5001 channels
// and nodes (a full batch plus a tail of one) and 10002
// addresses (two full batches plus a tail of two) must all
// land — a flush that drops its batch, or a missing tail
// flush, shows up as a short count.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestBatchBoundariesLoseNothing(t *testing.T) {
	conn, _, _ := newTestDatabase(t)

	const n = 5001
	graph := &fakeGraph{}
	for i := 0; i < n; i++ {
		graph.channels = append(graph.channels, syntheticChannel(t, i))
		graph.nodes = append(graph.nodes, syntheticNode(i, fmt.Sprintf("node-%d", i)))
	}

	runImport(t, graph, conn)

	if got := countRows(t, conn, "channel_announcements"); got != n {
		t.Errorf("channel_announcements = %d rows, want %d", got, n)
	}
	if got := countRows(t, conn, "node_announcements"); got != n {
		t.Errorf("node_announcements = %d rows, want %d", got, n)
	}
	if got := countRows(t, conn, "node_addresses"); got != 2*n {
		t.Errorf("node_addresses = %d rows, want %d", got, 2*n)
	}
}








// -----------------------------------------------------------
// TestUndecodableAddressAsLongAsTheProtocolAllows
// -----------------------------------------------------------
//
// An address of a type LND cannot decode is stored as the
// hex of the rest of the node's address list, and the
// gossip protocol lets that list fill a whole
// node_announcement. At that maximum the address column
// must take every hex character — when it was a
// VARCHAR(255), MySQL's strict mode rejected the whole
// batch instead — and the unique key, which covers only
// the first 255 characters, must still see one row after a
// re-import.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestUndecodableAddressAsLongAsTheProtocolAllows(t *testing.T) {
	conn, _, _ := newTestDatabase(t)

	// The whole address list is one entry of a type LND does
	// not know, so all of it lands in one opaque blob
	payload := make([]byte, maxAddressListBytes)
	payload[0] = 0x0a

	node := syntheticNode(1, "long")
	node.Addresses = []net.Addr{&lnwire.OpaqueAddrs{Payload: payload}}
	graph := &fakeGraph{nodes: []*lndmodels.Node{node}}

	runImport(t, graph, conn)
	runImport(t, graph, conn)

	var rows, length int
	if err := conn.QueryRow("SELECT COUNT(*), MAX(CHAR_LENGTH(address)) FROM node_addresses").Scan(&rows, &length); err != nil {
		t.Fatalf("read node_addresses: %v", err)
	}
	if rows != 1 {
		t.Errorf("node_addresses = %d rows, want 1", rows)
	}
	if length != 2*maxAddressListBytes {
		t.Errorf("stored address = %d characters, want all %d", length, 2*maxAddressListBytes)
	}
}
