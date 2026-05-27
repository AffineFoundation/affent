import { describe, expect, it } from "vitest";
import { describeSourceAccess, sourceEvidenceLabel } from "./sourceAccess";

describe("describeSourceAccess", () => {
  it("classifies rendered dynamic partial, network, discovery, and verified sources", () => {
    const partial = describeSourceAccess("SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence\nPAGE TEXT:\nMarket Cap");
    expect(partial).toMatchObject({
      accessedUrl: "https://taostats.io/subnets/120",
      status: "dynamic_partial",
    });
    expect(partial ? sourceEvidenceLabel(partial) : "").toBe("partial source");

    expect(describeSourceAccess("SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch\nJSON_PATH: $.data.items[0].price\n\"0.06342 T\"")).toMatchObject({
      status: "network",
      accessedUrl: "https://taostats.io/api/subnets/120",
      requestedUrl: "https://taostats.io/subnets/120",
      ref: "n1",
      httpStatus: "200",
      contentType: "application/json",
      jsonPath: "$.data.items[0].price",
    });

    expect(describeSourceAccess("SourceAccess: browser_rendered_url=https://search.example/?q=affine; page_text_below=search_results_discovery_only\nPAGE TEXT:\nresult")).toMatchObject({
      status: "discovery_only",
    });

    expect(describeSourceAccess("SourceAccess: fetched_url=https://example.com/report; linked_urls_in_content=discovered_unverified_until_fetched\nreport")).toMatchObject({
      status: "verified",
      urlField: "fetched_url",
    });
  });

  it("keeps compatibility with older dynamic diagnostics", () => {
    expect(describeSourceAccess("SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=verified_page_evidence\nPAGE DIAGNOSTICS:\n- empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value")).toMatchObject({
      status: "dynamic_partial",
    });
  });
});
