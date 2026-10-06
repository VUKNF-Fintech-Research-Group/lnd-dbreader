// -----------------------------------------------------------
//  [*] fixturegen-v0.19.3 — writes the LND v0.19.3 test graph
//
//  One-off recorder for the regression suite: builds a small
//  channel graph with LND v0.19.3's OWN graph store — the
//  version the production node ran until the v0.21.4
//  upgrade — and writes it to the channel.db path given as
//  the only argument. ../channel-lnd-v0.19.3.db is this
//  program's output; ../golden-v0.19.3.json holds the rows
//  the v0.19.3 dbreader image produced from that file.
//
//  A Go module of its own, pinned to LND v0.19.3 (which
//  cannot share a build with v0.21.4), and under testdata/,
//  so the go command never builds or tests it with the
//  service. The SAME graph, written by v0.21.4, is built at
//  test time by writeFixtureGraph in ../../helpers_test.go —
//  keep the two in step: the golden comparison fails the
//  moment they drift.
//
//  The graph (node keys are the compressed pubkeys of the
//  private keys 0x01…01 to 0x04…04):
//
//    alpha   — announced, the source node; IPv4, IPv6,
//              Tor v3 and Tor v2 addresses, signed TLV
//    bravo   — announced, multibyte alias; IPv4 plus a DNS
//              hostname, which v0.19.3 can only keep as an
//              opaque blob (address type 5 did not exist)
//    charlie — announced with an EMPTY alias; one address of
//              an unknown future type (10)
//    delta   — never announced: the shell node LND creates
//              for the alpha–delta channel
//
//    850000x1500x1 alpha–bravo   both policies, one with an
//                                inbound-fee TLV, the other
//                                disabled
//    860000x2000x0 alpha–delta   no policies, short TLV
//    870000x3x2    bravo–charlie one policy, 142-byte TLV
//                                (its hex outgrows the 255
//                                key prefix)
//
//  It only ever runs in a throwaway golang:1.23 container
//  (the Go LND v0.19.3 pins); tests/README.md has the
//  recipe.
// -----------------------------------------------------------


package main

import (
	// Standard library
	"bytes"
	"fmt"
	"image/color"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	// btcd
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"

	// LND v0.19.3
	graphdb "github.com/lightningnetwork/lnd/graph/db"
	"github.com/lightningnetwork/lnd/graph/db/models"
	"github.com/lightningnetwork/lnd/kvdb"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/tor"
)








// -----------------------------------------------------------
// vertex
// -----------------------------------------------------------
//
// The compressed pubkey of the private key that is `fill`
// repeated 32 times — real curve points, so the fixture
// looks like a graph LND could have gossiped.
//
// Used by:
//   - main (below) — node and bitcoin keys
// -----------------------------------------------------------

func vertex(fill byte) [33]byte {
	_, pub := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{fill}, 32))

	var v [33]byte
	copy(v[:], pub.SerializeCompressed())
	return v
}








// -----------------------------------------------------------
// ordered
// -----------------------------------------------------------
//
// The two keys in BOLT 7 order — node_id_1 is the smaller
// one — so the channels look like real announcements.
//
// Used by:
//   - main (below) — every channel
// -----------------------------------------------------------

func ordered(a, b [33]byte) ([33]byte, [33]byte) {
	if bytes.Compare(a[:], b[:]) < 0 {
		return a, b
	}
	return b, a
}








// -----------------------------------------------------------
// main
// -----------------------------------------------------------
//
// Builds the graph in the file header into a NEW bolt file
// at os.Args[1] — an existing file is refused, the store
// would merge into it. NoFreelistSync is on, as on the LND
// node (lncfg.DefaultDB), so the file reopens like a copy
// of the real channel.db.
//
// Used by:
//   - a developer, once per re-recording (tests/README.md)
// -----------------------------------------------------------

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: %s <output channel.db>", os.Args[0])
	}
	out := os.Args[1]
	if _, err := os.Stat(out); err == nil {
		log.Fatalf("%s exists — remove it first", out)
	}


	// STEP 1: a fresh bolt file and LND's graph store over it
	// =======================================================
	backend, err := kvdb.GetBoltBackend(&kvdb.BoltBackendConfig{
		DBPath:         filepath.Dir(out),
		DBFileName:     filepath.Base(out),
		NoFreelistSync: true,
		DBTimeout:      kvdb.DefaultDBTimeout,
	})
	if err != nil {
		log.Fatalf("open %s: %v", out, err)
	}
	defer backend.Close()

	store, err := graphdb.NewKVStore(backend)
	if err != nil {
		log.Fatalf("graph store: %v", err)
	}


	// STEP 2: the three announced nodes; alpha is also the
	// source node, whose "source" key ForEachNode must skip
	// =====================================================
	features := lnwire.NewFeatureVector(lnwire.NewRawFeatureVector(
		lnwire.DataLossProtectRequired,
		lnwire.GossipQueriesOptional,
		lnwire.TLVOnionPayloadRequired,
		lnwire.StaticRemoteKeyRequired,
		lnwire.PaymentAddrRequired,
	), lnwire.Features)

	alpha := &models.LightningNode{
		PubKeyBytes:          vertex(0x01),
		HaveNodeAnnouncement: true,
		LastUpdate:           time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Addresses: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("203.0.113.10").To4(), Port: 9735},
			&net.TCPAddr{IP: net.ParseIP("2001:db8::10"), Port: 9736},
			&tor.OnionAddr{OnionService: "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion", Port: 9735},
			&tor.OnionAddr{OnionService: "abcdefghijklmnop.onion", Port: 9735},
		},
		Color:           color.RGBA{R: 0x33, G: 0x99, B: 0xff},
		Alias:           "alpha",
		AuthSigBytes:    bytes.Repeat([]byte{0x31}, 64),
		Features:        features,
		ExtraOpaqueData: []byte{0x01, 0x03, 0xaa, 0xbb, 0xcc},
	}

	// The DNS hostname exactly as v0.19.3's wire decoder keeps
	// it: type 5, length, "node.example.com", port 9735
	dnsPayload := append([]byte{0x05, 16}, []byte("node.example.com")...)
	dnsPayload = append(dnsPayload, 0x26, 0x07)

	bravo := &models.LightningNode{
		PubKeyBytes:          vertex(0x02),
		HaveNodeAnnouncement: true,
		LastUpdate:           time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
		Addresses: []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("198.51.100.20").To4(), Port: 9735},
			&lnwire.OpaqueAddrs{Payload: dnsPayload},
		},
		Color:        color.RGBA{R: 0xff, G: 0x99, B: 0x00},
		Alias:        "⚡bravo",
		AuthSigBytes: bytes.Repeat([]byte{0x32}, 64),
		Features:     features,
	}

	charlie := &models.LightningNode{
		PubKeyBytes:          vertex(0x03),
		HaveNodeAnnouncement: true,
		LastUpdate:           time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		Addresses: []net.Addr{
			&lnwire.OpaqueAddrs{Payload: []byte{0x0a, 0xde, 0xad, 0xbe, 0xef}},
		},
		Color:        color.RGBA{R: 0x01, G: 0x02, B: 0x03},
		Alias:        "",
		AuthSigBytes: bytes.Repeat([]byte{0x33}, 64),
		Features:     features,
	}

	for _, node := range []*models.LightningNode{alpha, bravo, charlie} {
		if err := store.AddLightningNode(node); err != nil {
			log.Fatalf("add node %s: %v", node.Alias, err)
		}
	}
	if err := store.SetSourceNode(alpha); err != nil {
		log.Fatalf("source node: %v", err)
	}


	// STEP 3: the three channels — alpha–delta brings in delta
	// as a shell node, the store's own doing
	// ========================================================
	delta := vertex(0x04)

	// What v0.19.3's gossiper stores for a channel with no
	// feature bits: the encoded empty vector, 0x0000
	var noFeatures bytes.Buffer
	if err := lnwire.NewRawFeatureVector().Encode(&noFeatures); err != nil {
		log.Fatalf("encode features: %v", err)
	}

	longTLV := append([]byte{0x05, 140}, make([]byte, 140)...)
	for i := range longTLV[2:] {
		longTLV[2+i] = byte(i)
	}

	channels := []struct {
		scid       lnwire.ShortChannelID
		a, b       [33]byte
		btcFill    byte
		outpoint   byte
		capacity   btcutil.Amount
		extraBytes []byte
	}{
		{lnwire.ShortChannelID{BlockHeight: 850000, TxIndex: 1500, TxPosition: 1}, alpha.PubKeyBytes, bravo.PubKeyBytes, 0xa1, 0x01, 5_000_000, nil},
		{lnwire.ShortChannelID{BlockHeight: 860000, TxIndex: 2000, TxPosition: 0}, alpha.PubKeyBytes, delta, 0xb1, 0x02, 1_000_000, []byte{0x03, 0x04, 0xde, 0xad, 0xbe, 0xef}},
		{lnwire.ShortChannelID{BlockHeight: 870000, TxIndex: 3, TxPosition: 2}, bravo.PubKeyBytes, charlie.PubKeyBytes, 0xc1, 0x03, 20_000_000, longTLV},
	}

	edges := make([]*models.ChannelEdgeInfo, len(channels))
	for i, c := range channels {
		node1, node2 := ordered(c.a, c.b)
		edges[i] = &models.ChannelEdgeInfo{
			ChannelID:        c.scid.ToUint64(),
			ChainHash:        *chaincfg.MainNetParams.GenesisHash,
			NodeKey1Bytes:    node1,
			NodeKey2Bytes:    node2,
			BitcoinKey1Bytes: vertex(c.btcFill),
			BitcoinKey2Bytes: vertex(c.btcFill + 1),
			Features:         noFeatures.Bytes(),
			AuthProof: &models.ChannelAuthProof{
				NodeSig1Bytes:    bytes.Repeat([]byte{0x11}, 64),
				NodeSig2Bytes:    bytes.Repeat([]byte{0x12}, 64),
				BitcoinSig1Bytes: bytes.Repeat([]byte{0x13}, 64),
				BitcoinSig2Bytes: bytes.Repeat([]byte{0x14}, 64),
			},
			ChannelPoint:    wire.OutPoint{Hash: chainhash.Hash{c.outpoint}, Index: 1},
			Capacity:        c.capacity,
			ExtraOpaqueData: c.extraBytes,
		}
		if err := store.AddChannelEdge(edges[i]); err != nil {
			log.Fatalf("add channel %v: %v", c.scid, err)
		}
	}


	// STEP 4: the policies — direction bit 0 is node1's side;
	// 860000x2000x0 is left with none at all
	// =======================================================
	// Inbound fee TLV: type 55555 (BigSize fd d903), length 8,
	// base -100 msat, rate -50 ppm
	inboundFee := []byte{0xfd, 0xd9, 0x03, 0x08, 0xff, 0xff, 0xff, 0x9c, 0xff, 0xff, 0xff, 0xce}

	policies := []*models.ChannelEdgePolicy{
		{
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
		if _, _, err := store.UpdateEdgePolicy(policy); err != nil {
			log.Fatalf("update policy %d: %v", policy.ChannelID, err)
		}
	}

	fmt.Printf("wrote %s\n", out)
}
