//go:build liveweb

package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFetchTool_LiveTaoStatsSubnetEmbeddedData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tool := FetchTool(FetchConfig{})
	args, _ := json.Marshal(map[string]string{"url": "https://taostats.io/subnets/120"})
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, want := range []string{
		"Embedded data preview (page source evidence",
		`"netuid":120`,
		"Affine",
		`"market_cap"`,
		`"github_repo"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("live TaoStats output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Failure: kind=dynamic_shell") {
		t.Fatalf("live TaoStats page should surface embedded data instead of a no-evidence dynamic shell:\n%s", out)
	}
}
