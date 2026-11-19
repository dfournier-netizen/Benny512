package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestG3b2GDTFShareSettingsUI runs gdtf_settings_test.js: Settings › 7 GDTF
// Share and 9 About on the literal ui.js, gdtf-common.js and
// gdtf-settings.js (approved G3a mockup scenes 1–8; owner rounds 3 and 5).
func TestG3b2GDTFShareSettingsUI(t *testing.T) { runNodeTest(t, "gdtf_settings_test.js") }

// TestG3b2SettingsSectionsAndWords: the literal settings.js draws the
// sections in the approved order (… 6 Theme, 7 GDTF Share, 8 Danger zone,
// 9 About last: owner round 5 #2), says the GDTF buttons act at once (round
// 5 #3), tells the Art-Net adapter field that it also decides GDTF sync
// (round 3 #1), and says a full reset clears the login but keeps the files
// (round 3 #3). index.html loads the scripts and the stylesheet.
func TestG3b2SettingsSectionsAndWords(t *testing.T) {
	b, err := os.ReadFile("static/js/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	var order []string
	for _, m := range regexp.MustCompile(`<span class="b5-step-num">(\d+)</span> ([A-Za-z0-9 ]+)`).FindAllStringSubmatch(src, -1) {
		order = append(order, m[1]+" "+strings.TrimSpace(m[2]))
	}
	want := "1 Network|2 Universe numbering|3 sACN output|4 Output|5 Capture|6 Theme|7 GDTF Share|8 Danger zone|9 About Benny512"
	if strings.Join(order, "|") != want {
		t.Fatalf("Settings sections = %q\nwant %q", strings.Join(order, "|"), want)
	}
	for _, s := range []string{
		`id="gdtfShareRoot"`, `id="aboutRoot"`,
		"these buttons act at once, no Apply needed",
		"GDTF Share changes are saved as you make them.",
		"<strong>Also decides GDTF sync:</strong> sync never uses the card chosen here",
		"It also clears the GDTF Share login;\n            the downloaded GDTF files are kept.",
	} {
		if !strings.Contains(src, s) {
			t.Errorf("settings.js lacks %q", s)
		}
	}
	h, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(h)
	ui, common, settings, gs := strings.Index(html, `src="/js/ui.js"`), strings.Index(html, `src="/js/gdtf-common.js"`),
		strings.Index(html, `src="/js/settings.js"`), strings.Index(html, `src="/js/gdtf-settings.js"`)
	if ui < 0 || common < ui || settings < 0 || gs < settings || gs < common {
		t.Fatalf("index.html script order: ui %d, gdtf-common %d, settings %d, gdtf-settings %d", ui, common, settings, gs)
	}
	if !strings.Contains(html, `href="/css/screens-gdtf.css"`) {
		t.Fatal("index.html does not load screens-gdtf.css")
	}
	a, _ := os.ReadFile("static/js/app.js")
	if !strings.Contains(string(a), "if (typeof GdtfSettings !== 'undefined') GdtfSettings.init();") {
		t.Fatal("app.js does not start GdtfSettings")
	}
}
