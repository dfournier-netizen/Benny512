package walk

import (
	"sort"
	"testing"
	"time"
)

func TestFormatAddressRange(t *testing.T) {
	cases := []struct {
		name      string
		start     uint16
		footprint uint16
		known     bool
		want      string
	}{
		{"unknown address", 0, 0, false, "—"},
		{"unknown address with footprint", 141, 20, false, "—"},
		{"zero footprint (splitter/gateway)", 5, 0, true, "5"},
		{"footprint 1", 141, 1, true, "141 (141)"},
		{"footprint 20 — owner's exact example", 141, 20, true, "141 (141-160)"},
		{"footprint 19 would be 141-159, not 160 — arithmetic must be start+footprint-1", 141, 19, true, "141 (141-159)"},
		{"exactly fills to 512, no overflow", 500, 13, true, "500 (500-512)"},
		{"overflows past 512", 500, 20, true, "500 (500-519) ⚠ overflows past 512"},
		{"footprint 1 at the last channel", 512, 1, true, "512 (512)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatAddressRange(c.start, c.footprint, c.known)
			if got != c.want {
				t.Errorf("FormatAddressRange(%d,%d,%v) = %q, want %q", c.start, c.footprint, c.known, got, c.want)
			}
		})
	}
}

func TestLess_AddressCompoundKeyAndFootprintZeroLast(t *testing.T) {
	// Deliberately unsorted: mixes universes, addresses, and two
	// addressless devices (one *confirmed* footprint 0, one address
	// unknown) to prove the compound (universe, address) key and the
	// "addressless always last" rule together. The splitter has
	// FootprintKnown:true (DEVICE_INFO has actually reported footprint 0)
	// — that's what earns it the "no footprint" treatment, not merely
	// having Footprint's zero value.
	devs := []SortableDevice{
		{UID: "u5", PortAddress: 1, Address: 50, Footprint: 4, AddressKnown: true},
		{UID: "u1", PortAddress: 0, Address: 1, Footprint: 4, AddressKnown: true},
		{UID: "splitter", PortAddress: 0, Address: 0, Footprint: 0, FootprintKnown: true, AddressKnown: true},
		{UID: "u2", PortAddress: 0, Address: 21, Footprint: 4, AddressKnown: true},
		{UID: "unresolved", PortAddress: 0, Address: 0, Footprint: 4, AddressKnown: false},
		{UID: "u4", PortAddress: 1, Address: 1, Footprint: 4, AddressKnown: true},
	}
	sort.SliceStable(devs, func(i, j int) bool { return Less(devs[i], devs[j], OrderAddress) })

	wantOrder := []string{"u1", "u2", "u4", "u5", "splitter", "unresolved"}
	got := uidsOf(devs)
	if !equalStrings(got, wantOrder) {
		t.Fatalf("OrderAddress = %v, want %v", got, wantOrder)
	}
}

func TestLess_AddressDescKeepsFootprintZeroAtEnd(t *testing.T) {
	// The critical regression this guards: a naive "reverse everything"
	// descending sort would put addressless devices FIRST. They must stay
	// last in both directions (task ask).
	devs := []SortableDevice{
		{UID: "u1", PortAddress: 0, Address: 1, Footprint: 4, AddressKnown: true},
		{UID: "splitter", PortAddress: 0, Address: 0, Footprint: 0, FootprintKnown: true, AddressKnown: true},
		{UID: "u2", PortAddress: 0, Address: 21, Footprint: 4, AddressKnown: true},
		{UID: "u5", PortAddress: 1, Address: 50, Footprint: 4, AddressKnown: true},
	}
	sort.SliceStable(devs, func(i, j int) bool { return Less(devs[i], devs[j], OrderAddressDesc) })

	wantOrder := []string{"u5", "u2", "u1", "splitter"}
	got := uidsOf(devs)
	if !equalStrings(got, wantOrder) {
		t.Fatalf("OrderAddressDesc = %v, want %v", got, wantOrder)
	}
}

func TestLess_UnknownFootprintSortsByAddressNotLast(t *testing.T) {
	// Regression guard for the bug caught by
	// TestWalkStart_OrdersByAddressAndIdentifiesFirst in internal/web: two
	// freshly-discovered fixtures whose DEVICE_INFO has never been fetched
	// both default to Footprint 0 (Go zero value), but FootprintKnown is
	// also false for both — they must sort by their real DMX address, not
	// fall into the "no footprint" bucket and tiebreak on UID.
	devs := []SortableDevice{
		{UID: "2222:00000001", Address: 21, AddressKnown: true}, // Footprint/FootprintKnown both zero value
		{UID: "2222:00000002", Address: 1, AddressKnown: true},
	}
	sort.SliceStable(devs, func(i, j int) bool { return Less(devs[i], devs[j], OrderAddress) })
	want := []string{"2222:00000002", "2222:00000001"}
	if got := uidsOf(devs); !equalStrings(got, want) {
		t.Fatalf("OrderAddress (unknown footprint) = %v, want %v", got, want)
	}
}

func TestLess_Model(t *testing.T) {
	devs := []SortableDevice{
		{UID: "b", Model: "Zoom Spot"},
		{UID: "a", Model: "Aria"},
		{UID: "c", Model: "Aria"}, // same model as "a" — UID tiebreak
	}
	sort.SliceStable(devs, func(i, j int) bool { return Less(devs[i], devs[j], OrderModel) })
	want := []string{"a", "c", "b"}
	if got := uidsOf(devs); !equalStrings(got, want) {
		t.Fatalf("OrderModel = %v, want %v", got, want)
	}
}

func TestLess_UID(t *testing.T) {
	devs := []SortableDevice{{UID: "2222:00000002"}, {UID: "1900:00000001"}, {UID: "2222:00000001"}}
	sort.SliceStable(devs, func(i, j int) bool { return Less(devs[i], devs[j], OrderUID) })
	want := []string{"1900:00000001", "2222:00000001", "2222:00000002"}
	if got := uidsOf(devs); !equalStrings(got, want) {
		t.Fatalf("OrderUID = %v, want %v", got, want)
	}
}

func TestLess_Discovery(t *testing.T) {
	base := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	devs := []SortableDevice{
		{UID: "late", FirstSeen: base.Add(2 * time.Second)},
		{UID: "early", FirstSeen: base},
		{UID: "mid", FirstSeen: base.Add(time.Second)},
	}
	sort.SliceStable(devs, func(i, j int) bool { return Less(devs[i], devs[j], OrderDiscovery) })
	want := []string{"early", "mid", "late"}
	if got := uidsOf(devs); !equalStrings(got, want) {
		t.Fatalf("OrderDiscovery = %v, want %v", got, want)
	}
}

func TestParseOrder(t *testing.T) {
	cases := []struct {
		in     string
		want   Order
		wantOK bool
	}{
		{"", OrderAddress, true},
		{"address", OrderAddress, true},
		{"address_desc", OrderAddressDesc, true},
		{"model", OrderModel, true},
		{"uid", OrderUID, true},
		{"discovery", OrderDiscovery, true},
		{"bogus", "", false},
	}
	for _, c := range cases {
		got, ok := ParseOrder(c.in)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("ParseOrder(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func uidsOf(devs []SortableDevice) []string {
	out := make([]string, len(devs))
	for i, d := range devs {
		out[i] = d.UID
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
