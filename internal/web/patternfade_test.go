package web

import (
	"testing"
	"time"
)

func TestPatternFadeHTTPValidationAndNoOutput(t *testing.T) {
	h := newHarness(t)
	st := h.srv.RigCheck.PatternStatus()
	if st.FadeMS != 1000 {
		t.Fatal("default is not one second")
	}
	for _, body := range []map[string]any{{}, {"fadeMs": -1}, {"fadeMs": 30001}, {"fadeMs": 1e15}, {"fadeMs": nil}} {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/fade", body)
		if rr.Code != 422 {
			t.Fatalf("%v: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	for _, ms := range []int64{0, 250, 1000, 30000} {
		rr := doJSON(t, h.srv.Handler(), "POST", "/api/patch/rigcheck/pattern/fade", map[string]any{"fadeMs": ms})
		var got patternStatusJSON
		mustUnmarshal(t, rr, &got)
		if rr.Code != 200 || got.FadeMS != ms || got.OutputEnabled {
			t.Fatalf("%d %s", rr.Code, rr.Body.String())
		}
	}
	h.srv.RigCheck.Stop()
	if h.srv.RigCheck.PatternStatus().FadeMS != int64((30*time.Second).Milliseconds()) || h.srv.DMX.OutputRunning() {
		t.Fatal("setting lost or output started")
	}
}
