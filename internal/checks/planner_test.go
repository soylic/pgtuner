package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// helper: run planner check with given settings and optional fake OS.
func runPlannerWithOS(settings map[string]string, os osinspect.Inspector) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(settings),
		OS:       os,
	}
	return PlannerCheck{}.Run(context.Background(), p)
}

// helper: run planner check with given settings (no OS facts).
func runPlanner(settings map[string]string) []pgtuner.Advice {
	return runPlannerWithOS(settings, osinspect.NoopInspector{})
}

// healthyPlannerSettings returns all-good planner settings.
func healthyPlannerSettings() map[string]string {
	return map[string]string{
		"random_page_cost":          "1.1",
		"effective_io_concurrency":  "200",
		"default_statistics_target": "100",
	}
}

// TestPlannerRandomPageCostHigh: random_page_cost > 1.5 → Info.
func TestPlannerRandomPageCostHigh(t *testing.T) {
	s := healthyPlannerSettings()
	s["random_page_cost"] = "4.0" // PostgreSQL default, likely SSD
	got := runPlanner(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for random_page_cost=4.0, got %v", levelsOf(got))
	}
}

// TestPlannerRandomPageCostAtThreshold: random_page_cost == 1.5 → no flag (boundary exclusive).
func TestPlannerRandomPageCostAtThreshold(t *testing.T) {
	s := healthyPlannerSettings()
	s["random_page_cost"] = "1.5"
	got := runPlanner(s)
	for _, a := range got {
		if a.Level == pgtuner.Info && a.Title == "random_page_cost may be too high for SSD storage" {
			t.Fatalf("1.5 exactly should NOT trigger random_page_cost advice (threshold is > 1.5), got %v", got)
		}
	}
}

// TestPlannerRandomPageCostLow: random_page_cost = 1.1 → no flag.
func TestPlannerRandomPageCostLow(t *testing.T) {
	got := runPlanner(healthyPlannerSettings())
	for _, a := range got {
		if a.Level == pgtuner.Info && a.Title == "random_page_cost may be too high for SSD storage" {
			t.Fatalf("random_page_cost=1.1 should NOT trigger advice, got %v", got)
		}
	}
}

// TestPlannerEffectiveIOConcurrencyLow: effective_io_concurrency = 1 → Info.
func TestPlannerEffectiveIOConcurrencyLow(t *testing.T) {
	s := healthyPlannerSettings()
	s["effective_io_concurrency"] = "1"
	got := runPlanner(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for effective_io_concurrency=1, got %v", levelsOf(got))
	}
}

// TestPlannerEffectiveIOConcurrencyZero: effective_io_concurrency = 0 → Info (also ≤1).
func TestPlannerEffectiveIOConcurrencyZero(t *testing.T) {
	s := healthyPlannerSettings()
	s["effective_io_concurrency"] = "0"
	got := runPlanner(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for effective_io_concurrency=0, got %v", levelsOf(got))
	}
}

// TestPlannerDefaultStatisticsTargetLow: default_statistics_target < 100 → Info.
func TestPlannerDefaultStatisticsTargetLow(t *testing.T) {
	s := healthyPlannerSettings()
	s["default_statistics_target"] = "50"
	got := runPlanner(s)
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for default_statistics_target=50, got %v", levelsOf(got))
	}
}

// TestPlannerDefaultStatisticsTargetAtDefault: default_statistics_target = 100 → no flag.
func TestPlannerDefaultStatisticsTargetAtDefault(t *testing.T) {
	s := healthyPlannerSettings()
	s["default_statistics_target"] = "100"
	got := runPlanner(s)
	for _, a := range got {
		if a.Level == pgtuner.Info && a.Title == "default_statistics_target is below the recommended default" {
			t.Fatalf("100 should NOT trigger statistics_target advice, got %v", got)
		}
	}
}

// TestPlannerAllHealthy: all good settings → OK.
func TestPlannerAllHealthy(t *testing.T) {
	got := runPlanner(healthyPlannerSettings())
	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK for healthy planner settings, got %v", levelsOf(got))
	}
}

// ----- evalRandomPageCostStorage (pure function) ----------------------------
// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST

func TestEvalRPCStorage_SSDHighCost_SpecificInfo(t *testing.T) {
	// SSD + high random_page_cost → Info mentioning non-rotational storage.
	f := false
	a := evalRandomPageCostStorage(&f, 4.0)
	if a == nil || a.Level != pgtuner.Info {
		t.Fatalf("expected Info for SSD + rpc=4.0, got %v", a)
	}
	if !strings.Contains(a.Message, "non-rotational") {
		t.Fatalf("expected SSD-specific message, got: %s", a.Message)
	}
}

func TestEvalRPCStorage_SSDLowCost_NoAdvice(t *testing.T) {
	// SSD + low random_page_cost (1.1) → no advice.
	f := false
	a := evalRandomPageCostStorage(&f, 1.1)
	if a != nil {
		t.Fatalf("expected no advice for SSD + rpc=1.1, got %+v", a)
	}
}

func TestEvalRPCStorage_HDDLowCost_Info(t *testing.T) {
	// HDD + random_page_cost too low (1.1) → Info about spinning disk.
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
	tru := true
	a := evalRandomPageCostStorage(&tru, 1.1)
	if a == nil || a.Level != pgtuner.Info {
		t.Fatalf("expected Info for HDD + rpc=1.1, got %v", a)
	}
	if !strings.Contains(a.Message, "spinning-disk") {
		t.Fatalf("expected HDD-specific message, got: %s", a.Message)
	}
}

func TestEvalRPCStorage_HDDHighCost_NoAdvice(t *testing.T) {
	// HDD + random_page_cost=4.0 (appropriate for HDDs) → no advice.
	tru := true
	a := evalRandomPageCostStorage(&tru, 4.0)
	if a != nil {
		t.Fatalf("expected no advice for HDD + rpc=4.0, got %+v", a)
	}
}

func TestEvalRPCStorage_HDDAtBoundary_NoAdvice(t *testing.T) {
	// HDD + random_page_cost == 2.0 exactly → nil (boundary: condition is < 2.0, exclusive).
	// src: https://www.postgresql.org/docs/current/runtime-config-query.html#GUC-RANDOM-PAGE-COST
	tru := true
	a := evalRandomPageCostStorage(&tru, 2.0)
	if a != nil {
		t.Fatalf("expected no advice for HDD + rpc=2.0 (boundary, exclusive), got %+v", a)
	}
}

func TestEvalRPCStorage_Unknown_Generic(t *testing.T) {
	// Unknown storage type + high rpc → generic Info (original behaviour).
	a := evalRandomPageCostStorage(nil, 4.0)
	if a == nil || a.Level != pgtuner.Info {
		t.Fatalf("expected generic Info for unknown storage + rpc=4.0, got %v", a)
	}
}

func TestEvalRPCStorage_Unknown_LowCost_NoAdvice(t *testing.T) {
	// Unknown storage + low rpc (1.1, not > 1.5) → no advice.
	a := evalRandomPageCostStorage(nil, 1.1)
	if a != nil {
		t.Fatalf("expected no advice for unknown storage + rpc=1.1, got %+v", a)
	}
}

// ----- storageRotational -----------------------------------------------------

func TestStorageRotational_Empty(t *testing.T) {
	got := storageRotational(nil)
	if got != nil {
		t.Fatalf("expected nil for empty devices, got %v", *got)
	}
}

func TestStorageRotational_AllSSD(t *testing.T) {
	devices := []osinspect.StorageDevice{
		{Device: "nvme0n1", Rotational: false},
		{Device: "nvme1n1", Rotational: false},
	}
	got := storageRotational(devices)
	if got == nil || *got != false {
		t.Fatalf("expected false (SSD) for all-SSD devices")
	}
}

func TestStorageRotational_AllHDD(t *testing.T) {
	devices := []osinspect.StorageDevice{
		{Device: "sda", Rotational: true},
		{Device: "sdb", Rotational: true},
	}
	got := storageRotational(devices)
	if got == nil || *got != true {
		t.Fatalf("expected true (HDD) for all-HDD devices")
	}
}

func TestStorageRotational_Mixed_SSDTakesPrecedence(t *testing.T) {
	devices := []osinspect.StorageDevice{
		{Device: "sda", Rotational: true},
		{Device: "nvme0n1", Rotational: false},
	}
	got := storageRotational(devices)
	if got == nil || *got != false {
		t.Fatalf("expected false (SSD) when any device is non-rotational")
	}
}

// ----- PlannerCheck.Run with fakeOS ------------------------------------------

func TestPlannerSSDHighRPCSpecificMessage(t *testing.T) {
	// PlannerCheck.Run with SSD device → SSD-specific message.
	s := map[string]string{
		"random_page_cost":          "4.0",
		"effective_io_concurrency":  "200",
		"default_statistics_target": "100",
	}
	got := runPlannerWithOS(s, fakeOSWithSSD{})
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "non-rotational") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected SSD-specific Info message for rpc=4.0 + SSD, got %v", got)
	}
}

// fakeOSWithSSD wraps fakeOS and overrides StorageDevices to return one SSD.
type fakeOSWithSSD struct{ fakeOS }

func (f fakeOSWithSSD) StorageDevices() ([]osinspect.StorageDevice, error) {
	return []osinspect.StorageDevice{
		{Device: "nvme0n1", Rotational: false, FSType: "ext4"},
	}, nil
}
