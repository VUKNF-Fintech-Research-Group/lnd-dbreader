// -----------------------------------------------------------
//  [*] tests — reading channel.db fixtures (graph_test.go)
//
//  models.OpenChannelGraph — the production opener — over
//  both fixtures, no MySQL needed: the file LND v0.19.3
//  wrote must walk completely through v0.21.4's decoders
//  (that is the node's data on the day of the upgrade), the
//  file v0.21.4 writes must hand over the DNS hostname
//  v0.19.3 choked on, and the open must stay read-only and
//  fail cleanly, not panic, on a file that is not a graph.
// -----------------------------------------------------------


package tests

import (
	// Standard library
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	// LND
	lndmodels "github.com/lightningnetwork/lnd/graph/db/models"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/routing/route"

	// This module
	"lnd-dbreader/models"
)








// -----------------------------------------------------------
// walkedChannel
// -----------------------------------------------------------
//
// One channel as ForEachChannel handed it over — the edge
// and its node1/node2 policies (nil when absent).
//
// Used by:
//   - walkGraph, TestV0193FixtureWalksThroughV0214Decoders
//     (below)
// -----------------------------------------------------------

type walkedChannel struct {
	info    *lndmodels.ChannelEdgeInfo
	policy1 *lndmodels.ChannelEdgePolicy
	policy2 *lndmodels.ChannelEdgePolicy
}








// -----------------------------------------------------------
// walkGraph
// -----------------------------------------------------------
//
// Both walks, collected: channels keyed by "block:tx:out",
// nodes by pubkey. Either walk failing fails the test.
//
// Used by:
//   - TestV0193FixtureWalksThroughV0214Decoders,
//     TestV0214FixtureHandsOverTheDNSAddress,
//     TestOpeningLeavesTheFileUntouched (below)
// -----------------------------------------------------------

func walkGraph(t *testing.T, graph models.ChannelGraph) (map[string]walkedChannel, map[route.Vertex]*lndmodels.Node) {
	t.Helper()
	ctx := context.Background()

	channels := map[string]walkedChannel{}
	err := graph.ForEachChannel(ctx, func(info *lndmodels.ChannelEdgeInfo, policy1, policy2 *lndmodels.ChannelEdgePolicy) error {
		channels[lnwire.NewShortChanIDFromInt(info.ChannelID).String()] = walkedChannel{info, policy1, policy2}
		return nil
	}, func() {
		channels = map[string]walkedChannel{}
	})
	if err != nil {
		t.Fatalf("walk channels: %v", err)
	}

	nodes := map[route.Vertex]*lndmodels.Node{}
	err = graph.ForEachNode(ctx, func(node *lndmodels.Node) error {
		nodes[route.Vertex(node.PubKeyBytes)] = node
		return nil
	}, func() {
		nodes = map[route.Vertex]*lndmodels.Node{}
	})
	if err != nil {
		t.Fatalf("walk nodes: %v", err)
	}

	return channels, nodes
}








// -----------------------------------------------------------
// TestV0193FixtureWalksThroughV0214Decoders
// -----------------------------------------------------------
//
// The graph LND v0.19.3 stored, read by v0.21.4's store: all
// three channels with their policies where they exist —
// including the inbound-fee TLV v0.21.4 now parses out of
// the opaque bytes v0.19.3 kept unparsed, and the disabled
// flag — and all four nodes: the shell node with no alias or
// colour at all, bravo's hostname still the opaque blob
// v0.19.3 made of it, and the source node's "source" key
// skipped.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestV0193FixtureWalksThroughV0214Decoders(t *testing.T) {
	channels, nodes := walkGraph(t, openGraph(t, v0193Fixture(t)))


	// STEP 1: the channels and their policies
	// =======================================
	if len(channels) != 3 {
		t.Fatalf("channels = %d, want 3", len(channels))
	}

	alphaBravo := channels[scidAlphaBravo]
	if alphaBravo.policy1 == nil || alphaBravo.policy2 == nil {
		t.Fatalf("%s policies = %v / %v, want both", scidAlphaBravo, alphaBravo.policy1, alphaBravo.policy2)
	}
	if fee := alphaBravo.policy1.InboundFee.UnwrapOr(lnwire.Fee{}); alphaBravo.policy1.InboundFee.IsNone() || fee != (lnwire.Fee{BaseFee: -100, FeeRate: -50}) {
		t.Errorf("%s inbound fee = %+v, want base -100 rate -50", scidAlphaBravo, alphaBravo.policy1.InboundFee)
	}
	if !alphaBravo.policy2.IsDisabled() {
		t.Errorf("%s node2 policy not disabled", scidAlphaBravo)
	}

	if alphaDelta := channels[scidAlphaDelta]; alphaDelta.info == nil || alphaDelta.policy1 != nil || alphaDelta.policy2 != nil {
		t.Errorf("%s = %+v, want an edge with no policies", scidAlphaDelta, alphaDelta)
	}

	bravoCharlie := channels[scidBravoCharlie]
	if bravoCharlie.info == nil || (bravoCharlie.policy1 == nil) == (bravoCharlie.policy2 == nil) {
		t.Errorf("%s = %+v, want an edge with exactly one policy", scidBravoCharlie, bravoCharlie)
	}


	// STEP 2: the nodes — the source key is not one of them
	// =====================================================
	if len(nodes) != 4 {
		t.Fatalf("nodes = %d, want 4", len(nodes))
	}

	delta := nodes[deltaKey]
	if delta == nil || delta.HaveAnnouncement() || delta.Alias.IsSome() || delta.Color.IsSome() {
		t.Errorf("delta = %+v, want a shell node without alias or colour", delta)
	}

	bravo := nodes[bravoKey]
	if bravo == nil || len(bravo.Addresses) != 2 {
		t.Fatalf("bravo addresses = %v, want 2", bravo)
	}
	if opaque, ok := bravo.Addresses[1].(*lnwire.OpaqueAddrs); !ok || opaque.String() != fixtureOpaqueDNS {
		t.Errorf("bravo's hostname = %#v, want the opaque %s v0.19.3 stored", bravo.Addresses[1], fixtureOpaqueDNS)
	}
}








// -----------------------------------------------------------
// TestV0214FixtureHandsOverTheDNSAddress
// -----------------------------------------------------------
//
// The regression behind the upgrade: LND v0.20+ stores a
// DNS hostname as address type 5, which v0.19.3's decoder
// rejected — and one undecodable address aborted the WHOLE
// node walk. Through v0.21.4 it arrives as an
// lnwire.DNSAddress and the walk completes.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestV0214FixtureHandsOverTheDNSAddress(t *testing.T) {
	_, nodes := walkGraph(t, openGraph(t, v0214Fixture(t)))

	if len(nodes) != 4 {
		t.Fatalf("nodes = %d, want 4", len(nodes))
	}

	bravo := nodes[bravoKey]
	if bravo == nil || len(bravo.Addresses) != 2 {
		t.Fatalf("bravo addresses = %v, want 2", bravo)
	}
	dns, ok := bravo.Addresses[1].(*lnwire.DNSAddress)
	if !ok || dns.Hostname != fixtureHostname || dns.Port != 9735 {
		t.Errorf("bravo's hostname = %#v, want %s:9735 as a DNSAddress", bravo.Addresses[1], fixtureHostname)
	}
}








// -----------------------------------------------------------
// TestOpeningLeavesTheFileUntouched
// -----------------------------------------------------------
//
// The copy is opened READ-ONLY: a full walk of both kinds
// leaves every byte and the mtime as they were. The old
// read-write open committed a write transaction (the
// store's bucket check) and, worse, rebuilt the freelist by
// scanning every page — the step that panicked on a torn
// copy and restarted the container.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestOpeningLeavesTheFileUntouched(t *testing.T) {
	path := v0193Fixture(t)

	// An mtime in the past, so a write could not hide inside
	// the same clock tick
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	fingerprint := func() [32]byte {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return sha256.Sum256(data)
	}
	before := fingerprint()

	graph, closeGraph, err := models.OpenChannelGraph(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	walkGraph(t, graph)
	if err := closeGraph(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if after := fingerprint(); !bytes.Equal(before[:], after[:]) {
		t.Errorf("the walk changed the file's bytes")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("mtime moved from %v to %v", past, info.ModTime())
	}
}








// -----------------------------------------------------------
// TestOpenFailsCleanlyOnABadFile
// -----------------------------------------------------------
//
// What a failed or torn copy can look like — no file, an
// empty file, a file of garbage — comes back as an error
// from OpenChannelGraph, so that sync is logged and retried;
// never a panic, which would take the process down.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestOpenFailsCleanlyOnABadFile(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
	}{
		{"missing", nil},
		{"empty", []byte{}},
		{"garbage", bytes.Repeat([]byte{0xab}, 64*1024)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "channel.db")
			if c.content != nil {
				if err := os.WriteFile(path, c.content, 0600); err != nil {
					t.Fatalf("write: %v", err)
				}
			}

			graph, closeGraph, err := models.OpenChannelGraph(path)
			if err == nil {
				closeGraph()
				t.Fatalf("opened a %s file as a graph: %v", c.name, graph)
			}
		})
	}
}
