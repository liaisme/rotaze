package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rotaze/rotaze/pkg/types"
)

// withGeoAPI points geoAPIURL at a stub server for the duration of the test.
func withGeoAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := geoAPIURL
	geoAPIURL = srv.URL
	t.Cleanup(func() { geoAPIURL = old })
}

func TestBatchGeoLookup(t *testing.T) {
	withGeoAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var req []map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := make([]geoIPResult, len(req))
		for i, item := range req {
			switch item["query"] {
			case "8.8.8.8":
				resp[i].CountryCode = "US"
			case "1.1.1.1":
				resp[i].CountryCode = "AU"
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	result, err := batchGeoLookup(context.Background(), []string{"8.8.8.8", "1.1.1.1"})
	if err != nil {
		t.Fatalf("batchGeoLookup failed: %v", err)
	}
	if result["8.8.8.8"] != "US" {
		t.Errorf("expected 8.8.8.8 -> US, got %q", result["8.8.8.8"])
	}
	if result["1.1.1.1"] != "AU" {
		t.Errorf("expected 1.1.1.1 -> AU, got %q", result["1.1.1.1"])
	}
}

func TestBatchGeoLookup_APIError(t *testing.T) {
	withGeoAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := batchGeoLookup(context.Background(), []string{"8.8.8.8"}); err == nil {
		t.Error("batchGeoLookup should fail on API error")
	}
}

func TestBatchGeoLookup_InvalidJSON(t *testing.T) {
	withGeoAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	})
	if _, err := batchGeoLookup(context.Background(), []string{"8.8.8.8"}); err == nil {
		t.Error("batchGeoLookup should fail on invalid JSON")
	}
}

func TestEnrichCountries(t *testing.T) {
	withGeoAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var req []map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := make([]geoIPResult, len(req))
		for i, item := range req {
			if item["query"] == "8.8.8.8" {
				resp[i].CountryCode = "US"
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	nodes := []*types.Node{
		{Server: "8.8.8.8", Country: "unknown"},
		{Server: "203.0.113.1", Country: "US"}, // already set, untouched
	}

	EnrichCountries(context.Background(), nodes)

	if nodes[0].Country != "US" {
		t.Errorf("expected 8.8.8.8 enriched to US, got %q", nodes[0].Country)
	}
	if nodes[1].Country != "US" {
		t.Errorf("pre-set country should not be overwritten, got %q", nodes[1].Country)
	}
}

func TestEnrichCountries_EmptyInput(t *testing.T) {
	// All nodes already have countries -> no API call, no panic
	nodes := []*types.Node{{Server: "1.2.3.4", Country: "JP"}}
	EnrichCountries(context.Background(), nodes)
	if nodes[0].Country != "JP" {
		t.Errorf("country should be untouched, got %q", nodes[0].Country)
	}
}

func TestEnrichCountries_DownAPI(t *testing.T) {
	withGeoAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	nodes := []*types.Node{{Server: "8.8.4.4", Country: "unknown"}}
	EnrichCountries(context.Background(), nodes)
	if nodes[0].Country != "unknown" {
		t.Errorf("unreachable API should leave country unknown, got %q", nodes[0].Country)
	}
}
