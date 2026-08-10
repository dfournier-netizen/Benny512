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
// eventually reports the six pre-scripted fixtures, and that the Analyzer's
// capture feed has entries once the synthetic ArtDmx generator has had a
// moment to run.
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
	if len(fixtures) != 6 {
		t.Fatalf("expected 6 demo fixtures, got %d: %+v", len(fixtures), fixtures)
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
