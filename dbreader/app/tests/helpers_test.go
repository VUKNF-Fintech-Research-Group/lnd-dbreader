// -----------------------------------------------------------
//  [*] tests — shared fixtures and plumbing (helpers_test.go)
//
//  Everything the test files share: the two fixture graphs,
//  the golden rows, a throwaway MySQL database per test, the
//  row dump that is compared against them, a fake graph for
//  volume, and the runner for the service binary.
//
//  The ANCHORS live in testdata/ and were recorded during
//  the LND v0.19.3 → v0.21.4 upgrade:
//
//    channel-lnd-v0.19.3.db — the fixture graph, written by
//      LND v0.19.3's own store (fixturegen-v0.19.3/)
//    golden-v0.19.3.json    — every row the PRODUCTION
//      v0.19.3 dbreader image wrote for that file
//
//  writeFixtureGraph (below) writes the SAME graph with LND
//  v0.21.4's store. The two LND versions store it
//  identically except for bravo's DNS hostname, so
//  goldenForV0214 is the golden rows with exactly that one
//  delta applied — any other difference is a regression.
//
//  The MySQL layer needs DBREADER_TEST_MYSQL_HOST (port,
//  user and password default to 3306/root/root); without it
//  those tests skip. runTests.sh starts a throwaway MySQL
//  8.4.0 and sets it.
// -----------------------------------------------------------


package tests

import (
	// Standard library
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	// btcd
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"

	// LND
	graphdb "github.com/lightningnetwork/lnd/graph/db"
	lndmodels "github.com/lightningnetwork/lnd/graph/db/models"
	"github.com/lightningnetwork/lnd/kvdb"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/routing/route"
	"github.com/lightningnetwork/lnd/tor"

	// MySQL driver (registers itself)
	_ "github.com/go-sql-driver/mysql"

	// This module
	"lnd-dbreader/db"
	"lnd-dbreader/models"
)








// Bravo's DNS hostname — v0.21.4 decodes it, v0.19.3 kept
// the raw address bytes (type 5, length, name, port) as
// this opaque hex, port 0
const (
	fixtureHostname  = "node.example.com"
	fixtureOpaqueDNS = "05106e6f64652e6578616d706c652e636f6d2607"
)

// The fixture channels, as the walks and rows name them
const (
	scidAlphaBravo   = "850000:1500:1"
	scidAlphaDelta   = "860000:2000:0"
	scidBravoCharlie = "870000:3:2"
)

// The four fixture nodes' keys — the compressed pubkeys of
// the private keys 0x01…01 to 0x04…04 (see vertex)
var (
	alphaKey   = vertex(0x01)
	bravoKey   = vertex(0x02)
	charlieKey = vertex(0x03)
	deltaKey   = vertex(0x04)
)








// -----------------------------------------------------------
// vertex
// -----------------------------------------------------------
//
// The compressed pubkey of the private key made of the fill
// byte repeated 32 times — the same derivation the v0.19.3
// generator uses, so both fixtures share every key.
//
// Used by:
//   - writeFixtureGraph (below) — node and bitcoin keys
//   - the fixture key vars (above)
// -----------------------------------------------------------

func vertex(fill byte) route.Vertex {
	_, pub := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{fill}, 32))

	var v route.Vertex
	copy(v[:], pub.SerializeCompressed())
	return v
}








// -----------------------------------------------------------
// ordered
// -----------------------------------------------------------
//
// The two keys in BOLT 7 order — node_id_1 is the smaller.
//
// Used by:
//   - writeFixtureGraph (below) — every channel
// -----------------------------------------------------------

func ordered(a, b route.Vertex) (route.Vertex, route.Vertex) {
	if bytes.Compare(a[:], b[:]) < 0 {
		return a, b
	}
	return b, a
}








// -----------------------------------------------------------
// writeFixtureGraph
// -----------------------------------------------------------
//
// Writes the fixture graph into a NEW bolt file at path with
// LND v0.21.4's own store — what the production node writes
// after the upgrade. Field for field the graph in
// testdata/fixturegen-v0.19.3/main.go (whose header draws
// it), with one deliberate difference: bravo announces its
// hostname as an lnwire.DNSAddress, the type v0.19.3 did not
// have. Keep the two in step.
//
// Used by:
//   - v0214Fixture (below)
// -----------------------------------------------------------

func writeFixtureGraph(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()


	// STEP 1: a fresh bolt file — NoFreelistSync, as on the
	// node — and LND's graph store over it
	// =====================================================
	backend, err := kvdb.GetBoltBackend(&kvdb.BoltBackendConfig{
		DBPath:         filepath.Dir(path),
		DBFileName:     filepath.Base(path),
		NoFreelistSync: true,
		DBTimeout:      kvdb.DefaultDBTimeout,
	})
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer backend.Close()

	store, err := graphdb.NewKVStore(backend)
	if err != nil {
		t.Fatalf("graph store: %v", err)
	}


	// STEP 2: the three announced nodes; alpha is also the
	// source node, whose "source" key the walk must skip
	// ====================================================
	features := lnwire.NewRawFeatureVector(
		lnwire.DataLossProtectRequired,
		lnwire.GossipQueriesOptional,
		lnwire.TLVOnionPayloadRequired,
		lnwire.StaticRemoteKeyRequired,
		lnwire.PaymentAddrRequired,
	)

	alpha := lndmodels.NewV1Node(alphaKey, &lndmodels.NodeV1Fields{
		Addresses: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("203.0.113.10").To4(), Port: 9735},
			&net.TCPAddr{IP: net.ParseIP("2001:db8::10"), Port: 9736},
			&tor.OnionAddr{OnionService: "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion", Port: 9735},
			&tor.OnionAddr{OnionService: "abcdefghijklmnop.onion", Port: 9735},
		},
		AuthSigBytes:    bytes.Repeat([]byte{0x31}, 64),
		Features:        features,
		Color:           color.RGBA{R: 0x33, G: 0x99, B: 0xff},
		Alias:           "alpha",
		LastUpdate:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ExtraOpaqueData: []byte{0x01, 0x03, 0xaa, 0xbb, 0xcc},
	})

	bravo := lndmodels.NewV1Node(bravoKey, &lndmodels.NodeV1Fields{
		Addresses: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("198.51.100.20").To4(), Port: 9735},
			&lnwire.DNSAddress{Hostname: fixtureHostname, Port: 9735},
		},
		AuthSigBytes: bytes.Repeat([]byte{0x32}, 64),
		Features:     features,
		Color:        color.RGBA{R: 0xff, G: 0x99, B: 0x00},
		Alias:        "⚡bravo",
		LastUpdate:   time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	})

	charlie := lndmodels.NewV1Node(charlieKey, &lndmodels.NodeV1Fields{
		Addresses: []net.Addr{
			&lnwire.OpaqueAddrs{Payload: []byte{0x0a, 0xde, 0xad, 0xbe, 0xef}},
		},
		AuthSigBytes: bytes.Repeat([]byte{0x33}, 64),
		Features:     features,
		Color:        color.RGBA{R: 0x01, G: 0x02, B: 0x03},
		Alias:        "",
		LastUpdate:   time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
	})

	for _, node := range []*lndmodels.Node{alpha, bravo, charlie} {
		if err := store.AddNode(ctx, node); err != nil {
			t.Fatalf("add node %x: %v", node.PubKeyBytes, err)
		}
	}
	if err := store.SetSourceNode(ctx, alpha); err != nil {
		t.Fatalf("source node: %v", err)
	}


	// STEP 3: the three channels — alpha–delta brings in delta
	// as a shell node, the store's own doing
	// ========================================================
	longTLV := append([]byte{0x05, 140}, make([]byte, 140)...)
	for i := range longTLV[2:] {
		longTLV[2+i] = byte(i)
	}

	channels := []struct {
		scid     lnwire.ShortChannelID
		a, b     route.Vertex
		btcFill  byte
		outpoint byte
		capacity btcutil.Amount
		extra    []byte
	}{
		{lnwire.ShortChannelID{BlockHeight: 850000, TxIndex: 1500, TxPosition: 1}, alphaKey, bravoKey, 0xa1, 0x01, 5_000_000, nil},
		{lnwire.ShortChannelID{BlockHeight: 860000, TxIndex: 2000, TxPosition: 0}, alphaKey, deltaKey, 0xb1, 0x02, 1_000_000, []byte{0x03, 0x04, 0xde, 0xad, 0xbe, 0xef}},
		{lnwire.ShortChannelID{BlockHeight: 870000, TxIndex: 3, TxPosition: 2}, bravoKey, charlieKey, 0xc1, 0x03, 20_000_000, longTLV},
	}

	edges := make([]*lndmodels.ChannelEdgeInfo, len(channels))
	for i, c := range channels {
		node1, node2 := ordered(c.a, c.b)
		edge, err := lndmodels.NewV1Channel(
			c.scid.ToUint64(), *chaincfg.MainNetParams.GenesisHash, node1, node2,
			&lndmodels.ChannelV1Fields{
				BitcoinKey1Bytes: vertex(c.btcFill),
				BitcoinKey2Bytes: vertex(c.btcFill + 1),
				ExtraOpaqueData:  c.extra,
			},
			lndmodels.WithChannelPoint(wire.OutPoint{Hash: chainhash.Hash{c.outpoint}, Index: 1}),
			lndmodels.WithCapacity(c.capacity),
			lndmodels.WithChanProof(lndmodels.NewV1ChannelAuthProof(
				bytes.Repeat([]byte{0x11}, 64),
				bytes.Repeat([]byte{0x12}, 64),
				bytes.Repeat([]byte{0x13}, 64),
				bytes.Repeat([]byte{0x14}, 64),
			)),
		)
		if err != nil {
			t.Fatalf("build channel %v: %v", c.scid, err)
		}
		if err := store.AddChannelEdge(ctx, edge); err != nil {
			t.Fatalf("add channel %v: %v", c.scid, err)
		}
		edges[i] = edge
	}


	// STEP 4: the policies — direction bit 0 is node1's side;
	// 860000x2000x0 is left with none at all
	// =======================================================
	// Inbound fee TLV: type 55555 (BigSize fd d903), length 8,
	// base -100 msat, rate -50 ppm
	inboundFee := []byte{0xfd, 0xd9, 0x03, 0x08, 0xff, 0xff, 0xff, 0x9c, 0xff, 0xff, 0xff, 0xce}

	policies := []*lndmodels.ChannelEdgePolicy{
		{
			Version:                   lnwire.GossipVersion1,
			SigBytes:                  bytes.Repeat([]byte{0x21}, 64),
			ChannelID:                 edges[0].ChannelID,
			LastUpdate:                time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC),
			MessageFlags:              lnwire.ChanUpdateRequiredMaxHtlc,
			ChannelFlags:              0,
			TimeLockDelta:             80,
			MinHTLC:                   1000,
			MaxHTLC:                   4_950_000_000,
			FeeBaseMSat:               1000,
			FeeProportionalMillionths: 100,
			ToNode:                    edges[0].NodeKey2Bytes,
			ExtraOpaqueData:           inboundFee,
		},
		{
			Version:                   lnwire.GossipVersion1,
			SigBytes:                  bytes.Repeat([]byte{0x22}, 64),
			ChannelID:                 edges[0].ChannelID,
			LastUpdate:                time.Date(2026, 5, 6, 8, 9, 10, 0, time.UTC),
			MessageFlags:              lnwire.ChanUpdateRequiredMaxHtlc,
			ChannelFlags:              lnwire.ChanUpdateDirection | lnwire.ChanUpdateDisabled,
			TimeLockDelta:             144,
			MinHTLC:                   1,
			MaxHTLC:                   4_950_000_000,
			FeeBaseMSat:               0,
			FeeProportionalMillionths: 1,
			ToNode:                    edges[0].NodeKey1Bytes,
		},
		{
			Version:                   lnwire.GossipVersion1,
			SigBytes:                  bytes.Repeat([]byte{0x23}, 64),
			ChannelID:                 edges[2].ChannelID,
			LastUpdate:                time.Date(2026, 5, 7, 9, 10, 11, 0, time.UTC),
			MessageFlags:              lnwire.ChanUpdateRequiredMaxHtlc,
			ChannelFlags:              0,
			TimeLockDelta:             40,
			MinHTLC:                   1000,
			MaxHTLC:                   1_000_000_000,
			FeeBaseMSat:               500,
			FeeProportionalMillionths: 250,
			ToNode:                    edges[2].NodeKey2Bytes,
		},
	}

	for _, policy := range policies {
		if _, _, err := store.UpdateEdgePolicy(ctx, policy); err != nil {
			t.Fatalf("update policy %d: %v", policy.ChannelID, err)
		}
	}
}








// -----------------------------------------------------------
// copyFile
// -----------------------------------------------------------
//
// Plain byte copy, failing the test on any error.
//
// Used by:
//   - v0193Fixture (below)
// -----------------------------------------------------------

func copyFile(t *testing.T, src, dst string) {
	t.Helper()

	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}








// -----------------------------------------------------------
// v0193Fixture
// -----------------------------------------------------------
//
// A private copy of testdata/channel-lnd-v0.19.3.db in the
// test's temp dir — the walks are read-only, but no test may
// ever be able to touch the committed anchor.
//
// Used by:
//   - graph_test.go, import_test.go
// -----------------------------------------------------------

func v0193Fixture(t *testing.T) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), "channel.db")
	copyFile(t, filepath.Join("testdata", "channel-lnd-v0.19.3.db"), dst)
	return dst
}








// -----------------------------------------------------------
// v0214Fixture
// -----------------------------------------------------------
//
// The fixture graph freshly written by LND v0.21.4 (see
// writeFixtureGraph) into the test's temp dir.
//
// Used by:
//   - graph_test.go, import_test.go, service_test.go
// -----------------------------------------------------------

func v0214Fixture(t *testing.T) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), "channel.db")
	writeFixtureGraph(t, dst)
	return dst
}








// -----------------------------------------------------------
// openGraph
// -----------------------------------------------------------
//
// models.OpenChannelGraph — the production opener — on the
// given file, closed when the test ends.
//
// Used by:
//   - graph_test.go, import_test.go
// -----------------------------------------------------------

func openGraph(t *testing.T, path string) models.ChannelGraph {
	t.Helper()

	graph, closeGraph, err := models.OpenChannelGraph(path)
	if err != nil {
		t.Fatalf("open graph %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := closeGraph(); err != nil {
			t.Errorf("close graph: %v", err)
		}
	})
	return graph
}








// -----------------------------------------------------------
// tableRows
// -----------------------------------------------------------
//
// The three tables' contents minus id, first_seen and
// last_seen — the shape of golden-v0.19.3.json and of
// dumpRows. Rows are kept sorted on each table's unique key
// so two dumps compare index by index. json_data holds the
// text MySQL stores, keys re-ordered by its JSON type. The
// row types sit in the same block — they only exist as
// tableRows' columns.
//
//   sortRows — the canonical order
//
// Used by:
//   - loadGolden, goldenForV0214, dumpRows,
//     assertRowsEqual (below)
// -----------------------------------------------------------

type (
	tableRows struct {
		Channels  []channelRow `json:"channel_announcements"`
		Nodes     []nodeRow    `json:"node_announcements"`
		Addresses []addressRow `json:"node_addresses"`
	}

	// One channel_announcements row
	channelRow struct {
		ShortChannelID  uint64 `json:"short_channel_id"`
		NodeID1         string `json:"node_id_1"`
		NodeID2         string `json:"node_id_2"`
		BitcoinKey1     string `json:"bitcoin_key_1"`
		BitcoinKey2     string `json:"bitcoin_key_2"`
		ExtraOpaqueData string `json:"extra_opaque_data"`
		JSONData        string `json:"json_data"`
	}

	// One node_announcements row
	nodeRow struct {
		NodeID   string `json:"node_id"`
		Alias    string `json:"alias"`
		RGBColor string `json:"rgb_color"`
		JSONData string `json:"json_data"`
	}

	// One node_addresses row
	addressRow struct {
		NodeID  string `json:"node_id"`
		Address string `json:"address"`
		Port    uint32 `json:"port"`
	}
)






// -----------------------------------------------------------
// tableRows.sortRows
// -----------------------------------------------------------
//
// Sorts every table on its unique key, in column order.
//
// Used by:
//   - loadGolden, goldenForV0214, dumpRows (below)
// -----------------------------------------------------------

func (r *tableRows) sortRows() {
	sort.Slice(r.Channels, func(i, j int) bool {
		a, b := r.Channels[i], r.Channels[j]
		if a.ShortChannelID != b.ShortChannelID {
			return a.ShortChannelID < b.ShortChannelID
		}
		if a.NodeID1 != b.NodeID1 {
			return a.NodeID1 < b.NodeID1
		}
		if a.NodeID2 != b.NodeID2 {
			return a.NodeID2 < b.NodeID2
		}
		return a.ExtraOpaqueData < b.ExtraOpaqueData
	})

	sort.Slice(r.Nodes, func(i, j int) bool {
		a, b := r.Nodes[i], r.Nodes[j]
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		if a.Alias != b.Alias {
			return a.Alias < b.Alias
		}
		return a.RGBColor < b.RGBColor
	})

	sort.Slice(r.Addresses, func(i, j int) bool {
		a, b := r.Addresses[i], r.Addresses[j]
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		return a.Port < b.Port
	})
}








// -----------------------------------------------------------
// loadGolden
// -----------------------------------------------------------
//
// testdata/golden-v0.19.3.json — the rows the production
// v0.19.3 dbreader image wrote for the v0.19.3 fixture.
// NEVER re-record it from the current code: it is the
// pre-upgrade truth. A deliberate behaviour change goes into
// the test as an explicit delta, like goldenForV0214.
//
// Used by:
//   - goldenForV0214 (below), import_test.go
// -----------------------------------------------------------

func loadGolden(t *testing.T) tableRows {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "golden-v0.19.3.json"))
	if err != nil {
		t.Fatalf("read golden rows: %v", err)
	}

	var rows tableRows
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("parse golden rows: %v", err)
	}
	rows.sortRows()
	return rows
}








// -----------------------------------------------------------
// goldenForV0214
// -----------------------------------------------------------
//
// The golden rows as the v0.21.4-written fixture must
// produce them: identical but for bravo's hostname, which
// v0.21.4 decodes — its address row becomes fixtureHostname
// with port 9735 instead of fixtureOpaqueDNS with port 0,
// and its JSON entry turns "tcp". The JSON edit matches the
// exact text MySQL stored; a golden file that no longer
// holds it fails here, loudly.
//
// Used by:
//   - import_test.go, service_test.go
// -----------------------------------------------------------

func goldenForV0214(t *testing.T) tableRows {
	t.Helper()

	rows := loadGolden(t)

	replaced := 0
	for i := range rows.Addresses {
		if rows.Addresses[i].Address == fixtureOpaqueDNS {
			rows.Addresses[i].Address = fixtureHostname
			rows.Addresses[i].Port = 9735
			replaced++
		}
	}

	opaqueEntry := `{"port": 0, "type": "unknown", "address": "` + fixtureOpaqueDNS + `"}`
	hostnameEntry := `{"port": 9735, "type": "tcp", "address": "` + fixtureHostname + `"}`
	for i := range rows.Nodes {
		if strings.Contains(rows.Nodes[i].JSONData, opaqueEntry) {
			rows.Nodes[i].JSONData = strings.Replace(rows.Nodes[i].JSONData, opaqueEntry, hostnameEntry, 1)
			replaced++
		}
	}

	if replaced != 2 {
		t.Fatalf("golden rows hold the opaque DNS address %d times, want 2 (one address row, one JSON entry)", replaced)
	}

	rows.sortRows()
	return rows
}








// -----------------------------------------------------------
// sameJSON
// -----------------------------------------------------------
//
// Two JSON texts decode to the same value — MySQL re-orders
// the keys of a JSON column and re-spaces it, so the text
// itself is not comparable to a fresh json.Marshal.
//
// Used by:
//   - assertRowsEqual (below)
// -----------------------------------------------------------

func sameJSON(a, b string) bool {
	var x, y interface{}
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}








// -----------------------------------------------------------
// assertRowsEqual
// -----------------------------------------------------------
//
// Table by table, row by row: every column must match, the
// json_data columns as decoded JSON (see sameJSON). Both
// sides must already be sorted (sortRows).
//
// Used by:
//   - import_test.go, service_test.go
// -----------------------------------------------------------

func assertRowsEqual(t *testing.T, want, got tableRows) {
	t.Helper()

	if len(got.Channels) != len(want.Channels) {
		t.Fatalf("channel_announcements: %d rows, want %d", len(got.Channels), len(want.Channels))
	}
	for i := range want.Channels {
		w, g := want.Channels[i], got.Channels[i]
		if !sameJSON(w.JSONData, g.JSONData) {
			t.Errorf("channel_announcements row %d json_data:\n got %s\nwant %s", i, g.JSONData, w.JSONData)
		}
		w.JSONData, g.JSONData = "", ""
		if w != g {
			t.Errorf("channel_announcements row %d:\n got %+v\nwant %+v", i, g, w)
		}
	}

	if len(got.Nodes) != len(want.Nodes) {
		t.Fatalf("node_announcements: %d rows, want %d", len(got.Nodes), len(want.Nodes))
	}
	for i := range want.Nodes {
		w, g := want.Nodes[i], got.Nodes[i]
		if !sameJSON(w.JSONData, g.JSONData) {
			t.Errorf("node_announcements row %d json_data:\n got %s\nwant %s", i, g.JSONData, w.JSONData)
		}
		w.JSONData, g.JSONData = "", ""
		if w != g {
			t.Errorf("node_announcements row %d:\n got %+v\nwant %+v", i, g, w)
		}
	}

	if len(got.Addresses) != len(want.Addresses) {
		t.Fatalf("node_addresses: %d rows, want %d\n got %+v\nwant %+v", len(got.Addresses), len(want.Addresses), got.Addresses, want.Addresses)
	}
	for i := range want.Addresses {
		if got.Addresses[i] != want.Addresses[i] {
			t.Errorf("node_addresses row %d:\n got %+v\nwant %+v", i, got.Addresses[i], want.Addresses[i])
		}
	}
}








// -----------------------------------------------------------
// mysqlConfig
// -----------------------------------------------------------
//
// Where the test MySQL is, split up the way the service
// binary wants it — its own MYSQL_HOST/PORT/USER/PASSWORD.
//
// Used by:
//   - mysqlServer, newTestDatabase (below), service_test.go
// -----------------------------------------------------------

type mysqlConfig struct {
	host, port, user, password string
}








// The first test to need MySQL waits for it, once per run;
// everyone after reuses the verdict
var mysqlReady struct {
	once sync.Once
	err  error
}








// -----------------------------------------------------------
// mysqlServer
// -----------------------------------------------------------
//
// The test MySQL server from the environment, as a DSN with
// no database, plus its parts. Skips the test when the
// server is not configured — the MySQL layer is
// runTests.sh's to provide. The first caller waits (up to 2
// minutes) for a freshly started container to accept
// connections.
//
// Used by:
//   - newTestDatabase (below)
// -----------------------------------------------------------

func mysqlServer(t *testing.T) (mysqlConfig, string) {
	t.Helper()

	cfg := mysqlConfig{
		host:     os.Getenv("DBREADER_TEST_MYSQL_HOST"),
		port:     envOr("DBREADER_TEST_MYSQL_PORT", "3306"),
		user:     envOr("DBREADER_TEST_MYSQL_USER", "root"),
		password: envOr("DBREADER_TEST_MYSQL_PASSWORD", "root"),
	}
	if cfg.host == "" {
		t.Skip("MySQL layer: DBREADER_TEST_MYSQL_HOST is unset — run ../runTests.sh")
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/", cfg.user, cfg.password, cfg.host, cfg.port)

	mysqlReady.once.Do(func() {
		conn, err := sql.Open("mysql", dsn)
		if err != nil {
			mysqlReady.err = err
			return
		}
		defer conn.Close()

		deadline := time.Now().Add(2 * time.Minute)
		for {
			if err = conn.Ping(); err == nil || time.Now().After(deadline) {
				mysqlReady.err = err
				return
			}
			time.Sleep(time.Second)
		}
	})
	if mysqlReady.err != nil {
		t.Fatalf("test MySQL at %s:%s never answered: %v", cfg.host, cfg.port, mysqlReady.err)
	}

	return cfg, dsn
}








// -----------------------------------------------------------
// envOr
// -----------------------------------------------------------
//
// os.Getenv with a default for an empty value.
//
// Used by:
//   - mysqlServer (above)
// -----------------------------------------------------------

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}








// Numbers the test databases, so two tests (or one run
// twice) never share a name
var databaseCounter atomic.Int64








// -----------------------------------------------------------
// newTestDatabase
// -----------------------------------------------------------
//
// A database of the test's own on the test MySQL — named
// after the test, dropped when it ends — and a connection
// to it with the same plain DSN shape main.go builds. Tables
// are NOT created: InitializeDatabaseTables does that, as on
// every production sync.
//
// Used by:
//   - import_test.go, service_test.go
// -----------------------------------------------------------

func newTestDatabase(t *testing.T) (*sql.DB, mysqlConfig, string) {
	t.Helper()

	cfg, serverDSN := mysqlServer(t)

	// MySQL caps names at 64 characters
	name := fmt.Sprintf("t%d_%s", databaseCounter.Add(1),
		strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(t.Name(), "_")))
	if len(name) > 64 {
		name = name[:64]
	}

	admin, err := sql.Open("mysql", serverDSN)
	if err != nil {
		t.Fatalf("connect to test MySQL: %v", err)
	}
	if _, err := admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		admin.Close()
		t.Fatalf("create database %s: %v", name, err)
	}

	conn, err := sql.Open("mysql", serverDSN+name)
	if err != nil {
		t.Fatalf("connect to %s: %v", name, err)
	}

	t.Cleanup(func() {
		conn.Close()
		if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
		admin.Close()
	})

	return conn, cfg, name
}








// -----------------------------------------------------------
// runImport
// -----------------------------------------------------------
//
// STEPs 3 and 4 of main.go's processLNDDatabase, in the same
// order: the tables, then channels, nodes, addresses.
//
// Used by:
//   - import_test.go
// -----------------------------------------------------------

func runImport(t *testing.T, graph models.ChannelGraph, conn *sql.DB) {
	t.Helper()

	if err := db.InitializeDatabaseTables(conn); err != nil {
		t.Fatalf("initialize tables: %v", err)
	}
	if err := db.SendChannelAnnouncements(graph, conn); err != nil {
		t.Fatalf("import channels: %v", err)
	}
	if err := db.SendNodeAnnouncements(graph, conn); err != nil {
		t.Fatalf("import nodes: %v", err)
	}
	if err := db.SendNodeAddresses(graph, conn); err != nil {
		t.Fatalf("import addresses: %v", err)
	}
}








// -----------------------------------------------------------
// dumpRows
// -----------------------------------------------------------
//
// Reads the three tables back into tableRows, sorted, for
// assertRowsEqual.
//
// Used by:
//   - import_test.go, service_test.go
// -----------------------------------------------------------

func dumpRows(t *testing.T, conn *sql.DB) tableRows {
	t.Helper()

	var rows tableRows


	// STEP 1: channel_announcements
	// =============================
	result, err := conn.Query(`SELECT short_channel_id, node_id_1, node_id_2, bitcoin_key_1, bitcoin_key_2, extra_opaque_data, json_data FROM channel_announcements`)
	if err != nil {
		t.Fatalf("read channel_announcements: %v", err)
	}
	for result.Next() {
		var r channelRow
		if err := result.Scan(&r.ShortChannelID, &r.NodeID1, &r.NodeID2, &r.BitcoinKey1, &r.BitcoinKey2, &r.ExtraOpaqueData, &r.JSONData); err != nil {
			t.Fatalf("scan channel row: %v", err)
		}
		rows.Channels = append(rows.Channels, r)
	}
	result.Close()


	// STEP 2: node_announcements
	// ==========================
	result, err = conn.Query(`SELECT node_id, alias, rgb_color, json_data FROM node_announcements`)
	if err != nil {
		t.Fatalf("read node_announcements: %v", err)
	}
	for result.Next() {
		var r nodeRow
		if err := result.Scan(&r.NodeID, &r.Alias, &r.RGBColor, &r.JSONData); err != nil {
			t.Fatalf("scan node row: %v", err)
		}
		rows.Nodes = append(rows.Nodes, r)
	}
	result.Close()


	// STEP 3: node_addresses
	// ======================
	result, err = conn.Query(`SELECT node_id, address, port FROM node_addresses`)
	if err != nil {
		t.Fatalf("read node_addresses: %v", err)
	}
	for result.Next() {
		var r addressRow
		if err := result.Scan(&r.NodeID, &r.Address, &r.Port); err != nil {
			t.Fatalf("scan address row: %v", err)
		}
		rows.Addresses = append(rows.Addresses, r)
	}
	result.Close()

	rows.sortRows()
	return rows
}








// -----------------------------------------------------------
// countRows
// -----------------------------------------------------------
//
// The number of rows in one table. The name is pasted into
// the query, so only the tests' own table names go in.
//
// Used by:
//   - import_test.go
// -----------------------------------------------------------

func countRows(t *testing.T, conn *sql.DB, table string) int {
	t.Helper()

	var n int
	if err := conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}








// -----------------------------------------------------------
// fakeGraph
// -----------------------------------------------------------
//
// A models.ChannelGraph over plain slices — for volumes and
// edits a bolt fixture would make slow or awkward. Walks
// call reset first, like the bolt store, and hand every
// channel over with no policies.
//
//   ForEachChannel — the channels, in slice order
//   ForEachNode    — the nodes, in slice order
//
// Used by:
//   - import_test.go — batch boundaries, renamed nodes
// -----------------------------------------------------------

type fakeGraph struct {
	channels []*lndmodels.ChannelEdgeInfo
	nodes    []*lndmodels.Node
}






// -----------------------------------------------------------
// fakeGraph.ForEachChannel
// -----------------------------------------------------------
//
// Calls reset once, as the bolt store does before its read
// transaction, then hands over every channel in slice order
// with both policies nil.
//
// Used by:
//   - db.SendChannelAnnouncements — via import_test.go
// -----------------------------------------------------------

func (f *fakeGraph) ForEachChannel(_ context.Context, cb func(*lndmodels.ChannelEdgeInfo, *lndmodels.ChannelEdgePolicy, *lndmodels.ChannelEdgePolicy) error, reset func()) error {
	reset()
	for _, channel := range f.channels {
		if err := cb(channel, nil, nil); err != nil {
			return err
		}
	}
	return nil
}






// -----------------------------------------------------------
// fakeGraph.ForEachNode
// -----------------------------------------------------------
//
// Calls reset once, then hands over every node in slice
// order.
//
// Used by:
//   - db.SendNodeAnnouncements, db.SendNodeAddresses — via
//     import_test.go
// -----------------------------------------------------------

func (f *fakeGraph) ForEachNode(_ context.Context, cb func(*lndmodels.Node) error, reset func()) error {
	reset()
	for _, node := range f.nodes {
		if err := cb(node); err != nil {
			return err
		}
	}
	return nil
}








// -----------------------------------------------------------
// syntheticVertex
// -----------------------------------------------------------
//
// A distinct, deterministic 33-byte key per i — not a curve
// point, which nothing on the import path checks.
//
// Used by:
//   - syntheticNode, syntheticChannel (below)
// -----------------------------------------------------------

func syntheticVertex(prefix byte, i int) route.Vertex {
	var v route.Vertex
	v[0] = prefix
	binary.BigEndian.PutUint32(v[29:], uint32(i))
	return v
}








// -----------------------------------------------------------
// syntheticNode
// -----------------------------------------------------------
//
// An announced v1 node with the given alias and two IPv4
// addresses derived from i.
//
// Used by:
//   - import_test.go
// -----------------------------------------------------------

func syntheticNode(i int, alias string) *lndmodels.Node {
	return lndmodels.NewV1Node(syntheticVertex(0x02, i), &lndmodels.NodeV1Fields{
		Addresses: []net.Addr{
			&net.TCPAddr{IP: net.IPv4(10, byte(i>>16), byte(i>>8), byte(i)).To4(), Port: 9735},
			&net.TCPAddr{IP: net.IPv4(10, byte(i>>16), byte(i>>8), byte(i)).To4(), Port: 9736},
		},
		AuthSigBytes: bytes.Repeat([]byte{0x40}, 64),
		Features:     lnwire.NewRawFeatureVector(),
		Color:        color.RGBA{R: 0x12, G: 0x34, B: 0x56},
		Alias:        alias,
		LastUpdate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	})
}








// -----------------------------------------------------------
// syntheticChannel
// -----------------------------------------------------------
//
// A v1 channel with a distinct scid and keys per i.
//
// Used by:
//   - import_test.go
// -----------------------------------------------------------

func syntheticChannel(t *testing.T, i int) *lndmodels.ChannelEdgeInfo {
	t.Helper()

	scid := lnwire.ShortChannelID{BlockHeight: 900000, TxIndex: uint32(i), TxPosition: 0}
	edge, err := lndmodels.NewV1Channel(
		scid.ToUint64(), *chaincfg.MainNetParams.GenesisHash,
		syntheticVertex(0x02, i), syntheticVertex(0x03, i),
		&lndmodels.ChannelV1Fields{
			BitcoinKey1Bytes: syntheticVertex(0x02, i+1_000_000),
			BitcoinKey2Bytes: syntheticVertex(0x03, i+1_000_000),
		},
	)
	if err != nil {
		t.Fatalf("build channel %d: %v", i, err)
	}
	return edge
}








// Without DBREADER_BIN the binary is built once per run and
// shared by every service test
var builtBinary struct {
	once sync.Once
	path string
	err  error
}








// -----------------------------------------------------------
// serviceBinary
// -----------------------------------------------------------
//
// The dbreader binary to run: DBREADER_BIN when set —
// runTests.sh points it at /lnd-dbreader, the very binary
// the production image copies — otherwise one built from
// this module into a temp dir.
//
// Used by:
//   - startService (below)
// -----------------------------------------------------------

func serviceBinary(t *testing.T) string {
	t.Helper()

	if bin := os.Getenv("DBREADER_BIN"); bin != "" {
		return bin
	}

	builtBinary.once.Do(func() {
		dir, err := os.MkdirTemp("", "dbreader-bin")
		if err != nil {
			builtBinary.err = err
			return
		}
		builtBinary.path = filepath.Join(dir, "lnd-dbreader")

		build := exec.Command("go", "build", "-o", builtBinary.path, ".")
		build.Dir = ".."
		if out, err := build.CombinedOutput(); err != nil {
			builtBinary.err = fmt.Errorf("%v\n%s", err, out)
		}
	})
	if builtBinary.err != nil {
		t.Fatalf("build the service binary: %v", builtBinary.err)
	}
	return builtBinary.path
}








// -----------------------------------------------------------
// service
// -----------------------------------------------------------
//
// One running dbreader process: its combined stdout+stderr
// is collected as it arrives (log writes stderr, the sync
// banners stdout) so a test can wait for a log line, then
// stop it the way docker stop does.
//
//   waitFor — block until a line contains a marker
//   stop    — SIGTERM, then the exit code
//   log     — the output so far
//
// startService (after the methods) launches one.
//
// The binary always copies to /tmp/channel_copy.db, so two
// services must never run at once — service tests stay
// sequential.
//
// Used by:
//   - service_test.go
// -----------------------------------------------------------

type service struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	output strings.Builder
	exited chan struct{}
}






// -----------------------------------------------------------
// service.waitFor
// -----------------------------------------------------------
//
// Polls the collected output until it contains marker;
// fails the test (with the whole log) on timeout or when
// the process exits first.
//
// Used by:
//   - service_test.go
// -----------------------------------------------------------

func (s *service) waitFor(t *testing.T, marker string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(s.log(), marker) {
			return
		}

		select {
		case <-s.exited:
			// The output may still be draining
			time.Sleep(100 * time.Millisecond)
			if strings.Contains(s.log(), marker) {
				return
			}
			t.Fatalf("service exited before %q:\n%s", marker, s.log())
		default:
		}

		if time.Now().After(deadline) {
			t.Fatalf("no %q after %v:\n%s", marker, timeout, s.log())
		}
		time.Sleep(50 * time.Millisecond)
	}
}






// -----------------------------------------------------------
// service.stop
// -----------------------------------------------------------
//
// SIGTERM — what docker stop sends — and the exit code,
// failing the test if the process does not exit within 15
// seconds.
//
// Used by:
//   - service_test.go
// -----------------------------------------------------------

func (s *service) stop(t *testing.T) int {
	t.Helper()

	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}

	select {
	case <-s.exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("service ignored SIGTERM for 15s:\n%s", s.log())
	}

	// The output may still be draining
	time.Sleep(100 * time.Millisecond)
	return s.cmd.ProcessState.ExitCode()
}






// -----------------------------------------------------------
// service.log
// -----------------------------------------------------------
//
// Everything the process printed so far.
//
// Used by:
//   - waitFor, stop (above), service_test.go
// -----------------------------------------------------------

func (s *service) log() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.String()
}








// -----------------------------------------------------------
// startService
// -----------------------------------------------------------
//
// Launches the service binary with the given extra
// environment (MYSQL_*, LND_DB_PATH, ...) on top of the
// test process's own. A reader goroutine keeps draining the
// output pipe — an unread io.Pipe would block the process —
// and a test that ends early kills it.
//
// Used by:
//   - service_test.go
// -----------------------------------------------------------

func startService(t *testing.T, env ...string) *service {
	t.Helper()

	reader, writer := io.Pipe()

	s := &service{
		cmd:    exec.Command(serviceBinary(t)),
		exited: make(chan struct{}),
	}
	s.cmd.Env = append(os.Environ(), env...)
	s.cmd.Stdout = writer
	s.cmd.Stderr = writer

	if err := s.cmd.Start(); err != nil {
		t.Fatalf("start the service: %v", err)
	}

	go func() {
		s.cmd.Wait()
		writer.Close()
		close(s.exited)
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			s.mu.Lock()
			s.output.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		select {
		case <-s.exited:
		default:
			s.cmd.Process.Kill()
			<-s.exited
		}
	})

	return s
}
