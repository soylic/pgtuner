package checks

import (
	"context"
	"testing"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func runConn(settings map[string]string, os osinspect.Inspector) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(settings),
		OS:       os,
	}
	return ConnectionCheck{}.Run(context.Background(), p)
}

func levelsOf(advices []pgtuner.Advice) []pgtuner.Level {
	out := make([]pgtuner.Level, len(advices))
	for i, a := range advices {
		out[i] = a.Level
	}
	return out
}

func hasLevel(advices []pgtuner.Advice, want pgtuner.Level) bool {
	for _, a := range advices {
		if a.Level == want {
			return true
		}
	}
	return false
}

// TestConnectionSSLOff checks that ssl=off always emits a Warn.
func TestConnectionSSLOff(t *testing.T) {
	got := runConn(map[string]string{
		"ssl":              "off",
		"listen_addresses": "localhost",
		"max_connections":  "100",
		"work_mem":         "4096",
	}, osinspect.NoopInspector{})

	if !hasLevel(got, pgtuner.Warn) {
		t.Fatalf("expected Warn for ssl=off, got %v", levelsOf(got))
	}
}

// TestConnectionListenAllNoSSL checks that listen_addresses='*' + ssl=off emits exactly ONE Warn
// (the combined "all interfaces without SSL" advisory, not a separate generic SSL-off advisory).
func TestConnectionListenAllNoSSL(t *testing.T) {
	got := runConn(map[string]string{
		"ssl":              "off",
		"listen_addresses": "*",
		"max_connections":  "100",
		"work_mem":         "4096",
	}, osinspect.NoopInspector{})

	warnCount := 0
	for _, a := range got {
		if a.Level == pgtuner.Warn {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Fatalf("expected exactly 1 Warn for listen_addresses=* + ssl=off, got %d warns in %v", warnCount, levelsOf(got))
	}
}

// TestConnectionHighMaxConnections checks that max_connections > 200 emits Info.
func TestConnectionHighMaxConnections(t *testing.T) {
	got := runConn(map[string]string{
		"ssl":              "on",
		"listen_addresses": "localhost",
		"max_connections":  "500",
		"work_mem":         "4096",
	}, osinspect.NoopInspector{})

	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for max_connections=500, got %v", levelsOf(got))
	}
}

// TestConnectionRAMExhaustionBad checks that a tiny-RAM host with large work_mem
// and many connections emits Bad.
func TestConnectionRAMExhaustionBad(t *testing.T) {
	// 200 connections × (1 GiB work_mem + 2 MiB) >> 4 GiB RAM
	got := runConn(map[string]string{
		"ssl":              "on",
		"listen_addresses": "localhost",
		"max_connections":  "200",
		"work_mem":         "1048576", // 1 GiB in kB
	}, fakeOS{
		effMem: 4 * 1024 * 1024 * 1024, // 4 GiB effective (unconstrained)
	})

	if !hasLevel(got, pgtuner.Bad) {
		t.Fatalf("expected Bad for RAM exhaustion scenario, got %v", levelsOf(got))
	}
}

// TestConnectionRAMOKNoExhaustion checks that modest settings on a large host emit OK.
func TestConnectionRAMOKNoExhaustion(t *testing.T) {
	// 100 connections × (4 MiB work_mem + 2 MiB) = 600 MiB; host 64 GiB → well under 80 %
	got := runConn(map[string]string{
		"ssl":              "on",
		"listen_addresses": "localhost",
		"max_connections":  "100",
		"work_mem":         "4096", // 4 MiB in kB
	}, fakeOS{
		effMem: 64 * 1024 * 1024 * 1024, // 64 GiB effective (unconstrained)
	})

	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK for healthy settings, got %v", levelsOf(got))
	}
}

// TestConnectionNoOSFactsGraceful checks that ErrNoOSFacts produces Info (not panic/error).
func TestConnectionNoOSFactsGraceful(t *testing.T) {
	got := runConn(map[string]string{
		"ssl":              "on",
		"listen_addresses": "localhost",
		"max_connections":  "200",
		"work_mem":         "65536",
	}, fakeOS{memErr: osinspect.ErrNoOSFacts})

	// Must not panic and should contain Info about the raw product.
	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info when OS facts unavailable, got %v", levelsOf(got))
	}
}
