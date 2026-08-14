package registry

import "benny512/internal/rdm"

// DeviceClass groups a discovered RDM responder for UI iconography/labeling
// (report §1.3). "Fixture" is one class among several, not the default
// assumption — a splitter, gateway/node, or wireless radio is exactly as
// first-class a Registry entry as a moving light, per the report's explicit
// design goal for feature (C). Vocabulary matters here (Dom's own terms):
// a Gateway/Node has "ports" and may be "BiDi" (bidirectional RDM); a
// Dimmer/Power device has "circuit"/"draw"/"W", not "channels".
type DeviceClass int

// Device classes.
const (
	ClassUnknown DeviceClass = iota
	ClassFixture
	ClassGatewayNode
	ClassSplitter
	ClassDimmerPower
	ClassWireless
	ClassController
	ClassOther
)

// String renders the class label the UI should display.
func (c DeviceClass) String() string {
	switch c {
	case ClassFixture:
		return "Fixture"
	case ClassGatewayNode:
		return "Gateway/Node"
	case ClassSplitter:
		return "Splitter"
	case ClassDimmerPower:
		return "Dimmer/Power"
	case ClassWireless:
		return "Wireless"
	case ClassController:
		return "Controller"
	case ClassOther:
		return "Other"
	default:
		return "Unknown"
	}
}

// ClassifyDevice implements report §1.3's grouping recipe:
//
//  1. isWirelessProxy (a non-empty PROXIED_DEVICES/PROXIED_DEVICE_COUNT
//     response) is the strongest signal for "this is a LumenRadio-style
//     radio" and overrides everything else — proxies routinely under-report
//     product_category.
//  2. product_category's top byte (family) is the primary signal otherwise.
//  3. When category is NOT_DECLARED (0x0000), PRODUCT_DETAIL_ID_LIST's
//     infrastructure-flavored entries (splitter/ethernet-node/wireless-
//     link/datapatch/merge/router/protocol-converter) are the fallback —
//     real-world gateways often leave category at its zero value and rely
//     on product detail or a sensible model string instead.
//
// dmxFootprint is accepted for parity with the report's "footprint==0 is a
// soft confirmatory signal" note but is not currently used to change the
// classification outcome by itself (a sensor-only accessory can also read
// footprint 0without being infrastructure) — callers may still want it for
// a secondary "don't show a level fader" UI decision independent of class.
func ClassifyDevice(category rdm.ProductCategory, details []rdm.ProductDetail, dmxFootprint uint16, isWirelessProxy bool) DeviceClass {
	if isWirelessProxy {
		return ClassWireless
	}
	switch category.Family() {
	case rdm.CategoryFixture, rdm.CategoryProjector:
		return ClassFixture
	case rdm.CategoryDimmer, rdm.CategoryPower:
		return ClassDimmerPower
	case rdm.CategoryControl, rdm.CategoryTest:
		return ClassController
	case rdm.CategoryData:
		for _, d := range details {
			if d == rdm.DetailSplitter {
				return ClassSplitter
			}
			if d == rdm.DetailWirelessLink {
				return ClassWireless
			}
		}
		return ClassGatewayNode
	}
	if category == rdm.CategoryNotDeclared {
		for _, d := range details {
			switch d {
			case rdm.DetailSplitter:
				return ClassSplitter
			case rdm.DetailEthernetNode, rdm.DetailDataPatch, rdm.DetailMerge, rdm.DetailProtocolConverter, rdm.DetailRouter:
				return ClassGatewayNode
			case rdm.DetailWirelessLink:
				return ClassWireless
			}
		}
		return ClassUnknown
	}
	return ClassOther
}
