package web

import (
	"strings"
	"testing"
	"time"

	"benny512/internal/autoread"
	"benny512/internal/registry"
	"benny512/internal/session"
)

// TestFixtureJSONWordingFollowsCause pins both sentences at the JSON
// boundary: a no-response pause must not mention a wireless proxy, and the
// proxy-refusal sentence stays exactly what it was.
func TestFixtureJSONWordingFollowsCause(t *testing.T) {
	retry := time.Date(2026, 10, 6, 15, 22, 48, 0, time.UTC)
	nr := toFixtureJSON(registry.Fixture{ProxyUnreachable: true, ProxyRetryAt: retry,
		UnreachableCause: session.CauseNoResponse}, autoread.StateUnknown, 0)
	if nr.UnreachableCause != "no-response" {
		t.Errorf("unreachableCause = %q, want no-response", nr.UnreachableCause)
	}
	if nr.UnreachableNote != "Not answering RDM. Other fixtures on the same port are answering, so the line is working. Benny512 has paused this one so the rest of the rig keeps running, and will check it again automatically." {
		t.Errorf("no-response note = %q", nr.UnreachableNote)
	}
	if strings.Contains(nr.UnreachableNote, "proxy") || strings.Contains(nr.UnreachableNote, "universe") {
		t.Errorf("no-response note blames a proxy or names a universe: %q", nr.UnreachableNote)
	}

	px := toFixtureJSON(registry.Fixture{ProxyUnreachable: true, ProxyRetryAt: retry,
		UnreachableCause: session.CauseProxyRefusal}, autoread.StateUnknown, 0)
	if px.UnreachableNote != "Not answering through its wireless proxy. Benny512 has paused it so the rest of the rig keeps running, and will try again automatically." {
		t.Errorf("proxy note changed: %q", px.UnreachableNote)
	}
	if px.UnreachableCause != "proxy-refusal" {
		t.Errorf("proxy unreachableCause = %q", px.UnreachableCause)
	}
	if ok := toFixtureJSON(registry.Fixture{}, autoread.StateUnknown, 0); ok.UnreachableCause != "" {
		t.Errorf("reachable fixture unreachableCause = %q, want empty", ok.UnreachableCause)
	}
}
