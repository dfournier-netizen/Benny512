package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDemoModeSmoke starts the demo wiring exactly as --demo does, hits
// /api/nodes over httptest, and expects the two pre-scripted fake nodes
// (architecture rev 5 §7 / brief item 7). It also checks /api/fixtures
// eventually reports the ten pre-scripted devices (five plain fixtures, one
// NACK-fallback fixture, a Chroma-Q-like fixture, a splitter, and the EN4/
// Aurora gateway "root" devices — see cmd/benny512/demo.go's
// buildDemoDevices), and that the Analyzer's capture feed has entries once
// the synthetic ArtDmx generator has had a moment to run.
func TestDemoModeSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := buildDemo(ctx)
	go srv.Run(ctx)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("GET /api/nodes: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var nodes []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 demo nodes, got %d: %+v", len(nodes), nodes)
	}

	resp2, err := http.Get(ts.URL + "/api/fixtures")
	if err != nil {
		t.Fatalf("GET /api/fixtures: %v", err)
	}
	defer resp2.Body.Close()
	var fixtures []map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&fixtures); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	if len(fixtures) != 10 {
		t.Fatalf("expected 10 demo devices, got %d: %+v", len(fixtures), fixtures)
	}

	// Wait for at least one synthetic ArtDmx capture entry and one publish
	// cycle so /api/capture/snapshot has something.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp3, err := http.Get(ts.URL + "/api/capture/snapshot")
		if err == nil {
			var entries []map[string]any
			json.NewDecoder(resp3.Body).Decode(&entries)
			resp3.Body.Close()
			if len(entries) > 0 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for demo ArtDmx capture entries")
}

// TestDemoSensorWarningAndDeviceClass exercises the two hardware-independent
// scenarios the task explicitly asked --demo mode to cover end-to-end
// (see cmd/benny512/demo.go's buildDemoDevices comments): a sensor reading
// outside its normal band, so the UI's gauge can show a warning state
// (report §1.2 — the Chroma-Q-like fixture's PSU temperature sensor), and
// non-fixture device-class classification (report §1.3 — the splitter's
// DATA_DISTRIBUTION category + SPLITTER product detail). Classification
// only resolves once DEVICE_INFO has actually been fetched over RDM at
// least once (internal/registry/registry.go's reclassify runs off real
// GET-completion events), so this test issues that GET itself rather than
// assuming NoteFixture alone populates it.
func TestDemoSensorWarningAndDeviceClass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := buildDemo(ctx)
	go srv.Run(ctx)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// --- sensor warning state (Chroma-Q-like fixture, UID 5370:00000001) ---
	resp, err := http.Get(ts.URL + "/api/device/5370:00000001/sensors")
	if err != nil {
		t.Fatalf("GET sensors: %v", err)
	}
	var readings []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&readings); err != nil {
		resp.Body.Close()
		t.Fatalf("decode sensors: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sensors status = %d, body = %+v", resp.StatusCode, readings)
	}
	if len(readings) != 2 {
		t.Fatalf("expected 2 sensor readings, got %d: %+v", len(readings), readings)
	}
	byNumber := map[float64]map[string]any{}
	for _, r := range readings {
		byNumber[r["number"].(float64)] = r
	}
	if inBand, _ := byNumber[0]["inNormalBand"].(bool); inBand {
		t.Fatalf("expected sensor 0 (PSU temp, present=85 outside [0,60]) to read outside its normal band: %+v", byNumber[0])
	}
	if inBand, _ := byNumber[1]["inNormalBand"].(bool); !inBand {
		t.Fatalf("expected sensor 1 (PSU voltage, present=235 inside [100,250]) to read inside its normal band: %+v", byNumber[1])
	}

	// --- device-class classification (splitter, UID 1900:00000002) ---
	// ClassifyDevice needs both signals: DEVICE_INFO's product_category
	// (0x0801, DATA_DISTRIBUTION — resolves to the coarser "Gateway/Node" on
	// its own per internal/registry/deviceclass.go) and
	// PRODUCT_DETAIL_ID_LIST's DetailSplitter entry (the fine-grained
	// signal that refines it to "Splitter") — so fetch both.
	for _, pid := range []string{"0060", "0070"} { // DEVICE_INFO, PRODUCT_DETAIL_ID_LIST
		resp2, err := http.Get(ts.URL + "/api/device/1900:00000002/param/" + pid)
		if err != nil {
			t.Fatalf("GET pid %s: %v", pid, err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("pid %s status = %d", pid, resp2.StatusCode)
		}
	}

	classDeadline := time.Now().Add(2 * time.Second)
	for {
		resp3, err := http.Get(ts.URL + "/api/fixtures")
		if err == nil {
			var fixtures []map[string]any
			json.NewDecoder(resp3.Body).Decode(&fixtures)
			resp3.Body.Close()
			for _, f := range fixtures {
				if f["uid"] == "1900:00000002" && f["class"] == "Splitter" {
					return
				}
			}
		}
		if time.Now().After(classDeadline) {
			t.Fatal("timed out waiting for splitter (1900:00000002) to classify as \"Splitter\"")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
