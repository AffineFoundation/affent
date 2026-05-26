package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestNetworkEvidenceLogCapturesSameSiteXHRFetchOnly(t *testing.T) {
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://taostats.io/subnets/120", proto.NetworkResourceTypeDocument)

	if _, ok := log.Add("https://taostats.io/api/subnets/120", 200, proto.NetworkResourceTypeFetch, http.Header{"Content-Type": {"application/json"}}, []byte(`{"name":"Affine","netuid":120,"price":"0.0631"}`)); !ok {
		t.Fatal("same-site JSON fetch should be captured")
	}
	if _, ok := log.Add("https://stats.g.doubleclick.net/g/collect", 200, proto.NetworkResourceTypeFetch, http.Header{"Content-Type": {"application/json"}}, []byte(`{"tracker":true}`)); ok {
		t.Fatal("third-party analytics response must not be captured")
	}
	if _, ok := log.Add("https://taostats.io/_next/static/chunks/app.js", 200, proto.NetworkResourceTypeScript, http.Header{"Content-Type": {"application/javascript"}}, []byte(`console.log("Affine")`)); ok {
		t.Fatal("script resources must not be captured as evidence")
	}
	if _, ok := log.Add("https://taostats.io/api/private", 403, proto.NetworkResourceTypeXHR, http.Header{"Content-Type": {"application/json"}}, []byte(`{"error":"blocked"}`)); ok {
		t.Fatal("HTTP error responses must not be captured as evidence")
	}

	got := log.Search("Affine", 10)
	if len(got) != 1 {
		t.Fatalf("Search returned %d entries, want 1: %+v", len(got), got)
	}
	if got[0].Ref != "n1" || !strings.Contains(string(got[0].Body), `"netuid":120`) {
		t.Fatalf("captured entry = %+v body=%s", got[0], got[0].Body)
	}
}

func TestNetworkEvidenceSearchFiltersToCurrentPageHost(t *testing.T) {
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://taostats.io/subnets/120", proto.NetworkResourceTypeDocument)
	if _, ok := log.Add("https://taostats.io/api/subnets/120", 200, proto.NetworkResourceTypeFetch, http.Header{"Content-Type": {"application/json"}}, []byte(`{"name":"Affine"}`)); !ok {
		t.Fatal("taostats response should be captured")
	}

	log.ObserveResponse("https://metrics.example/dashboard", proto.NetworkResourceTypeDocument)
	if _, ok := log.Add("https://metrics.example/api/current", 200, proto.NetworkResourceTypeFetch, http.Header{"Content-Type": {"application/json"}}, []byte(`{"name":"Helio"}`)); !ok {
		t.Fatal("metrics response should be captured")
	}

	if got := log.Search("", 10); len(got) != 1 || got[0].URL != "https://metrics.example/api/current" {
		t.Fatalf("Search should expose only current page host responses, got %+v", got)
	}
	if _, ok := log.Get("n1"); ok {
		t.Fatal("old page network refs must not be readable after navigating to a different host")
	}
	if got, ok := log.Get("n2"); !ok || got.URL != "https://metrics.example/api/current" {
		t.Fatalf("current page ref not readable: got=%+v ok=%v", got, ok)
	}
}

func TestNetworkEvidenceToolsSearchAndRead(t *testing.T) {
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://taostats.io/subnets/120", proto.NetworkResourceTypeDocument)
	log.Add("https://taostats.io/api/subnets/120", 200, proto.NetworkResourceTypeXHR, http.Header{"Content-Type": {"application/json; charset=utf-8"}}, []byte(`{"subnet":"Affine","netuid":120,"market_cap":"201.04K T"}`))
	s := &Session{network: log}

	searchTool := NetworkSearchTool(s)
	searchOut, err := searchTool.Execute(context.Background(), json.RawMessage(`{"query":"market_cap","max_results":5}`))
	if err != nil {
		t.Fatalf("browser_network: %v", err)
	}
	for _, want := range []string{
		"BROWSER NETWORK EVIDENCE",
		"n1 status=200 resource=xhr content_type=application/json",
		"market_cap",
		`json_paths: $.market_cap="201.04K T"`,
		"Next: call browser_network_read with the most relevant ref and json_path",
	} {
		if !strings.Contains(searchOut, want) {
			t.Fatalf("browser_network output missing %q:\n%s", want, searchOut)
		}
	}

	readTool := NetworkReadTool(s)
	readOut, err := readTool.Execute(context.Background(), json.RawMessage(`{"ref":"n1","max_bytes":128}`))
	if err != nil {
		t.Fatalf("browser_network_read: %v", err)
	}
	for _, want := range []string{
		"SourceAccess: browser_network_url=https://taostats.io/api/subnets/120",
		"source_method=network_xhr_fetch",
		`"market_cap":"201.04K T"`,
	} {
		if !strings.Contains(readOut, want) {
			t.Fatalf("browser_network_read output missing %q:\n%s", want, readOut)
		}
	}
}

func TestNetworkEvidenceSearchShowsNestedJSONPathHints(t *testing.T) {
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://taostats.io/subnets/120", proto.NetworkResourceTypeDocument)
	log.Add("https://taostats.io/api/subnets/120", 200, proto.NetworkResourceTypeXHR, http.Header{"Content-Type": {"application/json"}}, []byte(`{"data":{"items":[{"name":"Affine","metrics":{"market_cap":"201.04K T","volume_24h":"5.1K T"}}],"meta":{"source":"api"}}}`))
	s := &Session{network: log}

	searchOut, err := NetworkSearchTool(s).Execute(context.Background(), json.RawMessage(`{"query":"market_cap","max_results":1}`))
	if err != nil {
		t.Fatalf("browser_network: %v", err)
	}
	for _, want := range []string{
		`json_paths: $.data.items[0].metrics.market_cap="201.04K T"`,
		"Next: call browser_network_read with the most relevant ref and json_path",
	} {
		if !strings.Contains(searchOut, want) {
			t.Fatalf("nested json path output missing %q:\n%s", want, searchOut)
		}
	}
}

func TestNetworkEvidenceSearchTokenizesMetricLabelQueries(t *testing.T) {
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://taostats.io/subnets/120", proto.NetworkResourceTypeDocument)
	log.Add("https://taostats.io/api/subnets/120/metrics", 200, proto.NetworkResourceTypeFetch, http.Header{"Content-Type": {"application/json"}}, []byte(`{"netuid":120,"name":"Affine","price":"0.06342 T","market_cap":"201.04K T","fdv":"1.32M T"}`))
	s := &Session{network: log}

	searchOut, err := NetworkSearchTool(s).Execute(context.Background(), json.RawMessage(`{"query":"price market cap FDV volume supply TVL","max_results":3}`))
	if err != nil {
		t.Fatalf("browser_network multi-label query: %v", err)
	}
	for _, want := range []string{
		"n1 status=200 resource=fetch content_type=application/json",
		`json_paths: $.fdv="1.32M T"; $.market_cap="201.04K T"; $.price="0.06342 T"`,
		"Next: call browser_network_read with the most relevant ref and json_path",
	} {
		if !strings.Contains(searchOut, want) {
			t.Fatalf("multi-label network search missing %q:\n%s", want, searchOut)
		}
	}
}

func TestNetworkEvidenceReadJSONPathExtractsSubtree(t *testing.T) {
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://taostats.io/subnets/120", proto.NetworkResourceTypeDocument)
	log.Add("https://taostats.io/api/subnets/120", 200, proto.NetworkResourceTypeXHR, http.Header{"Content-Type": {"application/json"}}, []byte(`{"data":{"items":[{"name":"Affine","metrics":{"market_cap":"201.04K T","volume_24h":"5.1K T"}}],"meta":{"source":"api"}}}`))
	s := &Session{network: log}

	readOut, err := NetworkReadTool(s).Execute(context.Background(), json.RawMessage(`{"ref":"n1","json_path":"$.data.items[0].metrics.market_cap","max_bytes":128}`))
	if err != nil {
		t.Fatalf("browser_network_read json_path: %v", err)
	}
	for _, want := range []string{
		"SourceAccess: browser_network_url=https://taostats.io/api/subnets/120",
		"JSON_PATH: $.data.items[0].metrics.market_cap",
		`"201.04K T"`,
	} {
		if !strings.Contains(readOut, want) {
			t.Fatalf("json_path output missing %q:\n%s", want, readOut)
		}
	}
	if strings.Contains(readOut, "volume_24h") || strings.Contains(readOut, `"source":"api"`) {
		t.Fatalf("json_path output should not dump sibling fields:\n%s", readOut)
	}
}

func TestNetworkEvidenceReadJSONPathGuidesMissingPath(t *testing.T) {
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://metrics.example/dashboard", proto.NetworkResourceTypeDocument)
	log.Add("https://metrics.example/api/current", 200, proto.NetworkResourceTypeFetch, http.Header{"Content-Type": {"application/json"}}, []byte(`{"items":[]}`))
	s := &Session{network: log}

	_, err := NetworkReadTool(s).Execute(context.Background(), json.RawMessage(`{"ref":"n1","json_path":"items[0].price"}`))
	if err == nil {
		t.Fatal("missing json_path should error")
	}
	for _, want := range []string{"json_path", "Failure: kind=not_found", "read without json_path"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing-path error missing %q: %v", want, err)
		}
	}
}

func TestNetworkEvidenceToolsNoMatchesAndMissingRefGuideRecovery(t *testing.T) {
	s := &Session{network: NewNetworkEvidenceLog()}
	searchOut, err := NetworkSearchTool(s).Execute(context.Background(), json.RawMessage(`{"query":"Affine"}`))
	if err != nil {
		t.Fatalf("browser_network no-match should not error: %v", err)
	}
	if !strings.Contains(searchOut, "MATCHES: none") || !strings.Contains(searchOut, "mark hidden fields unverified") {
		t.Fatalf("no-match output missing recovery guidance:\n%s", searchOut)
	}

	_, err = NetworkReadTool(s).Execute(context.Background(), json.RawMessage(`{"ref":"n99"}`))
	if err == nil {
		t.Fatal("missing network ref should error")
	}
	for _, want := range []string{"Failure: kind=not_found", "browser_network", "dashboard has loaded"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing-ref error missing %q: %v", want, err)
		}
	}
}

func TestNetworkEvidenceCandidateFiltersBinaryAndOversizedBodies(t *testing.T) {
	if !networkEvidenceCandidate("https://example.com/api", 200, proto.NetworkResourceTypeFetch, "application/json", []byte(`{"ok":true}`)) {
		t.Fatal("JSON fetch should be a candidate")
	}
	if networkEvidenceCandidate("https://example.com/api", 200, proto.NetworkResourceTypeImage, "application/json", []byte(`{"ok":true}`)) {
		t.Fatal("non-XHR/fetch resources should not be candidates")
	}
	if networkEvidenceCandidate("https://example.com/api", 200, proto.NetworkResourceTypeFetch, "application/octet-stream", []byte{0, 1, 2, 3}) {
		t.Fatal("binary bodies should not be candidates")
	}
	if !networkEvidenceCandidate("https://example.com/api", 200, proto.NetworkResourceTypeFetch, "application/json", make([]byte, maxNetworkEvidenceBodyBytes+1)) {
		t.Fatal("oversized JSON bodies should be candidates so the evidence log can keep a bounded prefix")
	}
	log := NewNetworkEvidenceLog()
	log.ObserveResponse("https://example.com/dashboard", proto.NetworkResourceTypeDocument)
	entry, ok := log.Add("https://example.com/api/large", 200, proto.NetworkResourceTypeFetch, http.Header{"Content-Type": {"application/json"}}, make([]byte, maxNetworkEvidenceBodyBytes+1))
	if !ok {
		t.Fatal("oversized JSON fetch should be captured with truncation")
	}
	if len(entry.Body) != maxNetworkEvidenceBodyBytes {
		t.Fatalf("captured body length = %d, want %d", len(entry.Body), maxNetworkEvidenceBodyBytes)
	}
}
