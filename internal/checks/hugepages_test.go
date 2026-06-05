package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenRemedy/pgtuner/internal/osinspect"
	"github.com/OpenRemedy/pgtuner/internal/pg"
	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// helper: run hugepages check.
func runHugePages(settings map[string]string, os osinspect.Inspector, major, minor int) []pgtuner.Advice {
	p := &Probe{
		Settings: pg.NewSettings(settings),
		OS:       os,
		Version:  pg.ServerVersion{Major: major, Minor: minor},
	}
	return HugePagesCheck{}.Run(context.Background(), p)
}

// 2 MiB huge pages, the Linux default.
const hugePgSize = 2 * 1024 * 1024

// TestHugePagesInsufficientPG15: PG15+, shared_memory_size_in_huge_pages > HugePagesTotal → Info.
func TestHugePagesInsufficientPG15(t *testing.T) {
	got := runHugePages(map[string]string{
		"huge_pages":                       "on",
		"shared_memory_size_in_huge_pages": "512", // needs 512
	}, fakeOS{mem: osinspect.Memory{
		HugePageSize:   hugePgSize,
		HugePagesTotal: 100, // only 100 allocated
	}}, 15, 0)

	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for insufficient huge pages on PG15, got %v", levelsOf(got))
	}
}

// TestHugePagesAdequatePG15: enough huge pages → OK.
func TestHugePagesAdequatePG15(t *testing.T) {
	got := runHugePages(map[string]string{
		"huge_pages":                       "on",
		"shared_memory_size_in_huge_pages": "256",
	}, fakeOS{mem: osinspect.Memory{
		HugePageSize:   hugePgSize,
		HugePagesTotal: 512, // more than enough
	}}, 15, 4)

	if !hasLevel(got, pgtuner.OK) {
		t.Fatalf("expected OK for adequate huge pages on PG15, got %v", levelsOf(got))
	}
}

// TestHugePagesTryWarns: huge_pages=try → Info regardless of allocation.
func TestHugePagesTryWarns(t *testing.T) {
	got := runHugePages(map[string]string{
		"huge_pages":                       "try",
		"shared_memory_size_in_huge_pages": "256",
	}, fakeOS{mem: osinspect.Memory{
		HugePageSize:   hugePgSize,
		HugePagesTotal: 512,
	}}, 16, 0)

	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for huge_pages=try, got %v", levelsOf(got))
	}
	// Also verify one of the Info advisories mentions "try".
	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "try") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Info message mentioning 'try', got %v", got)
	}
}

// TestHugePagesPrePG15Estimation: PG14 uses shared_buffers estimate path.
func TestHugePagesPrePG15Estimation(t *testing.T) {
	// shared_buffers = 4096 blocks of 8kB = 4096 * 8192 = 32 MiB
	// needed pages = ceil(32 MiB / 2 MiB) = 16
	got := runHugePages(map[string]string{
		"huge_pages":     "on",
		"shared_buffers": "4096", // 32 MiB in 8kB blocks
	}, fakeOS{mem: osinspect.Memory{
		HugePageSize:   hugePgSize,
		HugePagesTotal: 5, // not enough (need 16)
	}}, 14, 10)

	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info for insufficient huge pages on PG14 estimation path, got %v", levelsOf(got))
	}
}

// TestHugePagesNoopInspector: no OS facts → Info explaining why we can't verify.
func TestHugePagesNoopInspector(t *testing.T) {
	got := runHugePages(map[string]string{
		"huge_pages":                       "on",
		"shared_memory_size_in_huge_pages": "256",
	}, fakeOS{memErr: osinspect.ErrNoOSFacts}, 16, 0)

	if !hasLevel(got, pgtuner.Info) {
		t.Fatalf("expected Info when OS facts unavailable, got %v", levelsOf(got))
	}
	for _, a := range got {
		if a.Level == pgtuner.Warn || a.Level == pgtuner.Bad {
			t.Fatalf("expected no Warn/Bad when OS facts unavailable, got %v", levelsOf(got))
		}
	}
}

// TestHugePagesDockerContainerInfo: Docker environment emits Info about
// vm.nr_hugepages being host-global.
// src: https://www.postgresql.org/docs/current/kernel-resources.html#LINUX-HUGE-PAGES
func TestHugePagesDockerContainerInfo(t *testing.T) {
	got := runHugePages(map[string]string{
		"huge_pages":                       "on",
		"shared_memory_size_in_huge_pages": "256",
	}, fakeOS{
		env: osinspect.Environment{Kind: osinspect.EnvDocker, CgroupVersion: 2},
		mem: osinspect.Memory{
			HugePageSize:   hugePgSize,
			HugePagesTotal: 512, // adequate
		},
	}, 16, 0)

	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "host-global") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Info about host-global vm.nr_hugepages in Docker, got %v", got)
	}
}

// TestHugePagesKubernetesContainerInfo: Kubernetes environment emits Info.
func TestHugePagesKubernetesContainerInfo(t *testing.T) {
	got := runHugePages(map[string]string{
		"huge_pages":                       "on",
		"shared_memory_size_in_huge_pages": "256",
	}, fakeOS{
		env: osinspect.Environment{Kind: osinspect.EnvKubernetes, CgroupVersion: 2},
		mem: osinspect.Memory{
			HugePageSize:   hugePgSize,
			HugePagesTotal: 512,
		},
	}, 16, 0)

	found := false
	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "kubernetes") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Info about host-global in Kubernetes, got %v", got)
	}
}

// TestHugePagesBaremetalNoContainerInfo: bare-metal should NOT emit container Info.
func TestHugePagesBaremetalNoContainerInfo(t *testing.T) {
	got := runHugePages(map[string]string{
		"huge_pages":                       "on",
		"shared_memory_size_in_huge_pages": "256",
	}, fakeOS{
		env: osinspect.Environment{Kind: osinspect.EnvBareMetal},
		mem: osinspect.Memory{
			HugePageSize:   hugePgSize,
			HugePagesTotal: 512,
		},
	}, 16, 0)

	for _, a := range got {
		if a.Level == pgtuner.Info && strings.Contains(a.Message, "host-global") {
			t.Fatalf("bare-metal should NOT emit container host-global Info, got %v", got)
		}
	}
}
