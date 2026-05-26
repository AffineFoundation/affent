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

func TestFormatSourceAccessLine(t *testing.T) {
	got := FormatSourceAccessLine("browser_rendered_url", "https://example.com/final", "https://example.com/start", "page_text_below=verified_page_evidence", "; snapshot_id=7")
	want := "SourceAccess: browser_rendered_url=https://example.com/final; requested_url=https://example.com/start; page_text_below=verified_page_evidence; snapshot_id=7\n"
	if got != want {
		t.Fatalf("FormatSourceAccessLine() = %q, want %q", got, want)
	}
}
