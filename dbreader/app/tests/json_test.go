// -----------------------------------------------------------
//  [*] tests — JSON rendering anchors (json_test.go)
//
//  The flat JSON models.go writes into the json_data
//  columns, pinned without a database: the keys of each
//  document, how every address type renders, the
//  byte-reversed chain hash, the "block:tx:out" channel id
//  and the dropped empty opaque data. MySQL re-orders the
//  keys of a JSON column on the way in, so these compare
//  decoded values; import_test.go compares the rows MySQL
//  stored with the golden ones.
// -----------------------------------------------------------


package tests

import (
	// Standard library
	"encoding/json"
	"net"
	"reflect"
	"sort"
	"testing"

	// btcd
	"github.com/btcsuite/btcd/chaincfg"

	// LND
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/tor"

	// This module
	"lnd-dbreader/models"
)








// -----------------------------------------------------------
// jsonKeys
// -----------------------------------------------------------
//
// The sorted top-level keys of a JSON object.
//
// Used by:
//   - TestNodeJSONKeys (below)
// -----------------------------------------------------------

func jsonKeys(t *testing.T, raw []byte) []string {
	t.Helper()

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}

	keys := make([]string, 0, len(doc))
	for key := range doc {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}








// -----------------------------------------------------------
// TestNodeJSONKeys
// -----------------------------------------------------------
//
// The node document carries exactly node_id, alias,
// addresses, timestamp and rgb_color — features and opaque
// data never reach it — and an announcement without
// addresses gets an empty array, not null.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestNodeJSONKeys(t *testing.T) {
	raw, err := json.Marshal(models.CustomNodeAnnouncement{
		NodeAnnouncement1: lnwire.NodeAnnouncement1{
			Features:        lnwire.NewRawFeatureVector(lnwire.DataLossProtectRequired),
			ExtraOpaqueData: []byte{0x01, 0x03, 0xaa, 0xbb, 0xcc},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := []string{"addresses", "alias", "node_id", "rgb_color", "timestamp"}
	if got := jsonKeys(t, raw); !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}

	var doc struct {
		Addresses []models.CustomAddress `json:"addresses"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Addresses == nil {
		t.Errorf("addresses decoded as null in %s, want []", raw)
	}
}








// -----------------------------------------------------------
// TestNodeJSONRendersEveryAddressType
// -----------------------------------------------------------
//
// Every address type the graph can hand over, as one entry
// of the node document's "addresses": IPv4, IPv6 (brackets
// gone) and Tor split into "tcp" host/port; a DNS hostname
// does too — new with the v0.21.4 upgrade, v0.19.3 never
// decoded one; a type LND cannot decode stays "unknown",
// raw hex, port 0.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestNodeJSONRendersEveryAddressType(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		want models.CustomAddress
	}{
		{"ipv4", &net.TCPAddr{IP: net.ParseIP("203.0.113.10").To4(), Port: 9735}, models.CustomAddress{Type: "tcp", Address: "203.0.113.10", Port: 9735}},
		{"ipv6", &net.TCPAddr{IP: net.ParseIP("2001:db8::10"), Port: 9736}, models.CustomAddress{Type: "tcp", Address: "2001:db8::10", Port: 9736}},
		{"tor v3", &tor.OnionAddr{OnionService: "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion", Port: 9735}, models.CustomAddress{Type: "tcp", Address: "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion", Port: 9735}},
		{"tor v2", &tor.OnionAddr{OnionService: "abcdefghijklmnop.onion", Port: 9735}, models.CustomAddress{Type: "tcp", Address: "abcdefghijklmnop.onion", Port: 9735}},
		{"dns hostname", &lnwire.DNSAddress{Hostname: fixtureHostname, Port: 9735}, models.CustomAddress{Type: "tcp", Address: fixtureHostname, Port: 9735}},
		{"opaque", &lnwire.OpaqueAddrs{Payload: []byte{0x0a, 0xde, 0xad, 0xbe, 0xef}}, models.CustomAddress{Type: "unknown", Address: "0adeadbeef", Port: 0}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(models.CustomNodeAnnouncement{
				NodeAnnouncement1: lnwire.NodeAnnouncement1{Addresses: []net.Addr{c.addr}},
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var doc struct {
				Addresses []models.CustomAddress `json:"addresses"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("unmarshal %s: %v", raw, err)
			}
			if len(doc.Addresses) != 1 || doc.Addresses[0] != c.want {
				t.Errorf("addresses = %+v, want [%+v]", doc.Addresses, c.want)
			}
		})
	}
}








// -----------------------------------------------------------
// TestChannelJSONLayout
// -----------------------------------------------------------
//
// The channel document: chain_hash byte-REVERSED, the
// digits block explorers print for the mainnet genesis
// block; short_channel_id as "block:tx:out"; and
// extra_opaque_data only when there is some, as hex.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestChannelJSONLayout(t *testing.T) {
	announcement := &lnwire.ChannelAnnouncement1{
		ChainHash:      *chaincfg.MainNetParams.GenesisHash,
		ShortChannelID: lnwire.ShortChannelID{BlockHeight: 850000, TxIndex: 1500, TxPosition: 1},
	}

	render := func() map[string]string {
		raw, err := json.Marshal(models.CustomChannelAnnouncement{ChannelAnnouncement1: announcement})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var doc map[string]string
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		return doc
	}

	doc := render()
	if got, want := doc["chain_hash"], "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"; got != want {
		t.Errorf("chain_hash = %s, want %s", got, want)
	}
	if got := doc["short_channel_id"]; got != scidAlphaBravo {
		t.Errorf("short_channel_id = %s, want %s", got, scidAlphaBravo)
	}
	if _, present := doc["extra_opaque_data"]; present {
		t.Errorf("extra_opaque_data present for an empty blob: %v", doc)
	}
	if len(doc) != 6 {
		t.Errorf("keys = %v, want the 6 announced fields", doc)
	}

	announcement.ExtraOpaqueData = []byte{0x03, 0x04, 0xde, 0xad, 0xbe, 0xef}
	if got := render()["extra_opaque_data"]; got != "0304deadbeef" {
		t.Errorf("extra_opaque_data = %q, want 0304deadbeef", got)
	}
}
