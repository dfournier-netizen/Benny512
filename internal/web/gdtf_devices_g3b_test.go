package web

import (
	"os"
	"strings"
	"testing"
)

// TestG3b4DeviceGDTFGroupAndTags runs gdtf_devices_test.js: the Device
// inspector's "GDTF file for this device" group (found / likely /
// other-versions / no RDM match / no RDM data / zero ID, the server's words
// verbatim, footprint check option C, name search without guessing) and the
// cards' GDTF tags with their per-identity cache and one-at-a-time queue, on
// the literal ui.js, gdtf-common.js, gdtf-devices.js and devicedetail.js
// (approved G3a mockup scenes 12–16; owner rounds 4 and 5).
func TestG3b4DeviceGDTFGroupAndTags(t *testing.T) { runNodeTest(t, "gdtf_devices_test.js") }

// TestG3b4EveryDeviceCardHasAGDTFTag (owner round 5 #5): the literal
// devices.js puts the GDTF tag on every card, beside the class pill, and
// index.html loads gdtf-devices.js before devicedetail.js and devices.js.
func TestG3b4EveryDeviceCardHasAGDTFTag(t *testing.T) {
	b, err := os.ReadFile("static/js/devices.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	card := src[strings.Index(src, "function deviceCardHtml(f)"):]
	card = card[:strings.Index(card, "function renderPortSummaries")]
	if !strings.Contains(card, "${classPill(f.class)}\n          ${typeof GdtfDevices !== 'undefined' ? GdtfDevices.tagHtml(f) : ''}") {
		t.Fatal("deviceCardHtml does not put GdtfDevices.tagHtml(f) beside the class pill")
	}
	h, _ := os.ReadFile("static/index.html")
	html := string(h)
	g, dd, dv := strings.Index(html, `src="/js/gdtf-devices.js"`), strings.Index(html, `src="/js/devicedetail.js"`), strings.Index(html, `src="/js/devices.js"`)
	if g < 0 || g > dd || g > dv {
		t.Fatalf("index.html order: gdtf-devices %d, devicedetail %d, devices %d", g, dd, dv)
	}
}
