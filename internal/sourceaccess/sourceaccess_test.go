package sourceaccess

import "testing"

func TestParseLine(t *testing.T) {
	info := ParseLine("SourceAccess: browser_rendered_url=https://example.com/final; requested_url=https://example.com/start; page_text_below=not_found_page_discovery_only; rendered_browser_source_status=not_found_page_discovery_only; snapshot_id=7")
	if got, want := info.AccessedURL, "https://example.com/final"; got != want {
		t.Fatalf("AccessedURL = %q, want %q", got, want)
	}
	if got, want := info.RequestedURL, "https://example.com/start"; got != want {
		t.Fatalf("RequestedURL = %q, want %q", got, want)
	}
	if got, want := info.PageTextBelow, "not_found_page_discovery_only"; got != want {
		t.Fatalf("PageTextBelow = %q, want %q", got, want)
	}
	if got, want := info.RenderedBrowserSourceStatus, "not_found_page_discovery_only"; got != want {
		t.Fatalf("RenderedBrowserSourceStatus = %q, want %q", got, want)
	}
	if !info.IsDiscoveryOnly() {
		t.Fatal("IsDiscoveryOnly = false, want true")
	}
}

func TestAccessedURLFromResult(t *testing.T) {
	if got, want := AccessedURLFromResult("SourceAccess: fetched_url=https://example.com/data\nbody"), "https://example.com/data"; got != want {
		t.Fatalf("AccessedURLFromResult() = %q, want %q", got, want)
	}
	if got, want := AccessedURLFromResult("URL: https://example.com/page\nbody"), "https://example.com/page"; got != want {
		t.Fatalf("AccessedURLFromResult() = %q, want %q", got, want)
	}
}

func TestFirstInfoFromResult(t *testing.T) {
	info, ok := FirstInfoFromResult("noise\nSourceAccess: fetched_url=https://example.com/data; requested_url=https://example.com/start; page_text_below=search_results_discovery_only\nbody")
	if !ok {
		t.Fatal("FirstInfoFromResult() = false, want true")
	}
	if got, want := info.AccessedURL, "https://example.com/data"; got != want {
		t.Fatalf("AccessedURL = %q, want %q", got, want)
	}
	if got, want := info.PageTextBelow, "search_results_discovery_only"; got != want {
		t.Fatalf("PageTextBelow = %q, want %q", got, want)
	}
	if !info.IsDiscoveryOnly() {
		t.Fatal("IsDiscoveryOnly = false, want true")
	}
}

func TestParseLineNetworkSource(t *testing.T) {
	info := ParseLine("SourceAccess: browser_network_url=https://example.com/api/data; ref=n1; source_method=network_xhr_fetch")
	if got, want := info.URLField, "browser_network_url"; got != want {
		t.Fatalf("URLField = %q, want %q", got, want)
	}
	if got, want := info.AccessedURL, "https://example.com/api/data"; got != want {
		t.Fatalf("AccessedURL = %q, want %q", got, want)
	}
	if got, want := info.Ref, "n1"; got != want {
		t.Fatalf("Ref = %q, want %q", got, want)
	}
	if !info.IsNetworkSource() {
		t.Fatal("IsNetworkSource = false, want true")
	}
}

func TestFirstInfoFromResultCapturesNetworkJSONPath(t *testing.T) {
	info, ok := FirstInfoFromResult("SourceAccess: browser_network_url=https://example.com/api/data; ref=n1; source_method=network_xhr_fetch\nJSON_PATH: $.data.items[0].price\n\"0.06342 T\"")
	if !ok {
		t.Fatal("FirstInfoFromResult() = false, want true")
	}
	if got, want := info.Ref, "n1"; got != want {
		t.Fatalf("Ref = %q, want %q", got, want)
	}
	if got, want := info.JSONPath, "$.data.items[0].price"; got != want {
		t.Fatalf("JSONPath = %q, want %q", got, want)
	}
	if !info.IsNetworkSource() {
		t.Fatal("IsNetworkSource = false, want true")
	}
}

func TestHasDynamicPartialEvidence(t *testing.T) {
	result := "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120\nPAGE DIAGNOSTICS:\n- empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value"
	if !HasDynamicPartialEvidence(result) {
		t.Fatal("HasDynamicPartialEvidence = false, want true")
	}
	headerResult := "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence\nPAGE TEXT:\nMarket Cap"
	info, ok := FirstInfoFromResult(headerResult)
	if !ok || !info.IsDynamicPartial() || !HasDynamicPartialEvidence(headerResult) {
		t.Fatalf("partial dynamic header not recognized: info=%+v ok=%v", info, ok)
	}
	if HasDynamicPartialEvidence("SourceAccess: browser_network_url=https://example.com/api; source_method=network_xhr_fetch") {
		t.Fatal("network evidence without diagnostics should not be dynamic partial")
	}
}

func TestFormatSourceAccessLine(t *testing.T) {
	got := FormatSourceAccessLine("browser_rendered_url", "https://example.com/final", "https://example.com/start", "page_text_below=verified_page_evidence", "; snapshot_id=7")
	want := "SourceAccess: browser_rendered_url=https://example.com/final; requested_url=https://example.com/start; page_text_below=verified_page_evidence; snapshot_id=7\n"
	if got != want {
		t.Fatalf("FormatSourceAccessLine() = %q, want %q", got, want)
	}
}
