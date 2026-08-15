package walk

import "time"

// SortableDevice is the minimal set of fields Less needs to order one walk
// candidate against another — deliberately decoupled from registry.Fixture
// (and from Device) so this comparison logic is unit-testable in isolation,
// without any RDM/session plumbing (see sort_test.go). internal/web builds
// these from registry.Fixture plus its own resolved DMX address lookup (the
// live RDM round-trip lives outside this package, per the "owns no RDM/HTTP
// knowledge" rule in the package doc comment); the Devices screen's
// client-side JS sort mirrors this exact same rule set (see
// static/js/api.js's formatAddressRange/compareDevices) so the two lists
// never disagree about ordering.
type SortableDevice struct {
	UID         string
	Model       string
	PortAddress uint16 // Art-Net Port-Address ("universe") — the compound key's outer sort
	Address     uint16 // DMX start address — the compound key's inner sort
	Footprint   uint16 // DMX footprint; only meaningful when FootprintKnown
	// FootprintKnown is true once DEVICE_INFO has actually been fetched for
	// this device (registry.Fixture.HasDeviceInfo) — it's what lets Less
	// tell a *confirmed* zero-footprint device (a splitter/gateway, which
	// must sort last) apart from a device we simply haven't probed yet
	// (Footprint's zero value by default, which must NOT be treated as
	// "no footprint" — it has a perfectly good resolved DMX address and
	// belongs in the normal address order). Getting this wrong was a real
	// regression: two freshly-discovered fixtures with never-fetched
	// DEVICE_INFO both defaulted to Footprint 0 and fell through to the
	// addressless tiebreak (UID order) instead of their actual DMX
	// addresses (see TestWalkStart_OrdersByAddressAndIdentifiesFirst).
	FootprintKnown bool
	AddressKnown   bool // false when the start address could not be resolved at all
	FirstSeen      time.Time
}

// hasAddress reports whether d has a real, sortable DMX address. A device
// with a *confirmed* footprint of 0 (a splitter, gateway, or other
// zero-channel device — FootprintKnown true) or an unresolved start address
// sorts predictably to the end of any address-ordered list rather than
// interleaving arbitrarily at address 0 (task ask: "devices with no DMX
// footprint must sort predictably — group them at the end for address
// sorts, not interleaved arbitrarily"). A device whose footprint just
// hasn't been probed yet (FootprintKnown false) is not treated as
// footprint-0 — it sorts by its resolved address like any other fixture.
func (d SortableDevice) hasAddress() bool {
	if !d.AddressKnown {
		return false
	}
	if d.FootprintKnown && d.Footprint == 0 {
		return false
	}
	return true
}

// Less reports whether a should sort before b under order. This is the
// single source of truth every Rig Walk / Devices-table sort control routes
// through, so the compound-key and footprint-0-last rules can never drift
// between call sites.
func Less(a, b SortableDevice, order Order) bool {
	switch order {
	case OrderDiscovery:
		if !a.FirstSeen.Equal(b.FirstSeen) {
			return a.FirstSeen.Before(b.FirstSeen)
		}
		return a.UID < b.UID
	case OrderModel:
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.UID < b.UID
	case OrderUID:
		return a.UID < b.UID
	case OrderAddressDesc:
		return lessByAddress(a, b, true)
	case OrderAddress:
		return lessByAddress(a, b, false)
	default:
		return lessByAddress(a, b, false)
	}
}

// lessByAddress implements the (universe, start address) compound key.
// Addressed devices always sort before addressless ones regardless of
// desc — only the relative order *among* addressed devices flips with
// desc, which is what keeps the no-footprint group pinned to the end in
// BOTH directions instead of jumping to the front under a naive
// "reverse everything" descending sort.
func lessByAddress(a, b SortableDevice, desc bool) bool {
	aHas, bHas := a.hasAddress(), b.hasAddress()
	if aHas != bHas {
		return aHas
	}
	if !aHas {
		// Both addressless: a stable, deterministic fallback so this group
		// doesn't reorder arbitrarily between renders/requests.
		return a.UID < b.UID
	}
	if a.PortAddress != b.PortAddress {
		if desc {
			return a.PortAddress > b.PortAddress
		}
		return a.PortAddress < b.PortAddress
	}
	if a.Address != b.Address {
		if desc {
			return a.Address > b.Address
		}
		return a.Address < b.Address
	}
	// Tiebreak stays ascending regardless of direction — arbitrary but
	// deterministic, matching the addressless fallback above.
	return a.UID < b.UID
}

// ParseOrder validates an order string from a client request, defaulting to
// OrderAddress for "" and rejecting anything unrecognized (callers treat a
// false return as a 400).
func ParseOrder(s string) (Order, bool) {
	switch Order(s) {
	case "", OrderAddress:
		return OrderAddress, true
	case OrderAddressDesc, OrderModel, OrderUID, OrderDiscovery:
		return Order(s), true
	default:
		return "", false
	}
}
