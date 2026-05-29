package main

import (
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sse"
)

func TestValidateSessionRuntimeContractRequiresControlTools(t *testing.T) {
	reg := agent.NewRegistry()
	err := validateSessionRuntimeContract(reg, Config{EnableLoopProtocol: true})
	if err == nil {
		t.Fatal("validateSessionRuntimeContract succeeded, want missing tool error")
	}
	if !strings.Contains(err.Error(), agent.SessionScheduleToolName) || !strings.Contains(err.Error(), agent.LoopProtocolToolName) {
		t.Fatalf("error = %q, want both required control tools", err)
	}

	registerSessionScheduleTool(reg, newTestPool(t, 1, "5m"), "contract")
	agent.RegisterLoopProtocolOnly(reg, t.TempDir()+"/LOOP.md")
	if err := validateSessionRuntimeContract(reg, Config{EnableLoopProtocol: true}); err != nil {
		t.Fatalf("validateSessionRuntimeContract with required tools: %v", err)
	}
}

func TestSessionRuntimeContractSurfacesMissingExpectedCapabilities(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Add(&agent.Tool{Name: agent.SessionScheduleToolName, CatalogGroup: "Core"})
	contract := buildSessionRuntimeContract(&Session{registry: reg}, Config{EnableLoopProtocol: true})
	if contract.Status != "degraded" {
		t.Fatalf("status = %q, want degraded; contract=%+v", contract.Status, contract)
	}
	if !stringSliceContains(contract.Expected, "loop protocol") || !stringSliceContains(contract.Missing, "loop protocol") {
		t.Fatalf("contract = %+v, want loop protocol expected and missing", contract)
	}
	if !stringSliceContains(contract.Available, "schedules") || !stringSliceContains(contract.Available, "schedule runner") {
		t.Fatalf("available = %+v, want schedules and schedule runner", contract.Available)
	}
}

func TestRuntimeSurfaceCapabilityLabelsDistinguishScheduleRunner(t *testing.T) {
	surface := &sse.RuntimeSurfacePayload{
		Capabilities: sse.RuntimeCapabilities{
			SessionSchedule: true,
		},
	}
	available, unavailable := runtimeSurfaceCapabilityLabels(surface)
	if !stringSliceContains(available, "schedules") {
		t.Fatalf("available = %+v, want schedules", available)
	}
	if stringSliceContains(available, "schedule runner") {
		t.Fatalf("available = %+v, must not infer schedule runner from schedule tool", available)
	}
	if !stringSliceContains(unavailable, "schedule runner") {
		t.Fatalf("unavailable = %+v, want schedule runner", unavailable)
	}

	surface.Capabilities.SessionScheduleRunner = true
	available, unavailable = runtimeSurfaceCapabilityLabels(surface)
	if !stringSliceContains(available, "schedules") || !stringSliceContains(available, "schedule runner") {
		t.Fatalf("available = %+v, want schedules and schedule runner", available)
	}
	if stringSliceContains(unavailable, "schedule runner") {
		t.Fatalf("unavailable = %+v, want schedule runner removed", unavailable)
	}
}
