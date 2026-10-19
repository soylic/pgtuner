package checks

import (
	"context"
	"fmt"

	"github.com/soylic/pgtuner/internal/osinspect"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const docConnRuntime = "https://www.postgresql.org/docs/current/runtime-config-connection.html"

const docWorkMem = "https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-WORK-MEM"

// connBaselineBytes is the per-backend memory overhead beyond work_mem (stack,
// shared-library mappings, buffer rings). 2 MiB is a conservative lower bound.
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
const connBaselineBytes int64 = 2 * 1024 * 1024

// connPoolerThreshold is the max_connections value above which a pooler is
// recommended, because each backend holds shared-memory state and a slot.
// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
const connPoolerThreshold = 200

// ramExhaustFraction is the RAM fraction above which worst-case backend memory
// (max_connections × (work_mem + baseline)) is Bad. At 80 % the OOM killer is likely under load.
const ramExhaustFraction = 0.80

// ConnectionCheck evaluates connection, SSL, and backend-memory settings.
type ConnectionCheck struct{ clusterScoped }

func (ConnectionCheck) Name() string { return "connection" }

// Run emits degraded-path advice when OS facts are unavailable.
// It never panics and never returns an error.
func (ConnectionCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Connection"
	var out []pgtuner.Advice

	// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-SSL
	sslOn := p.Settings.Bool("ssl")
	listenAll := false
	if la, ok := p.Settings.Get("listen_addresses"); ok && la == "*" {
		listenAll = true
	}

	if listenAll && !sslOn {
		out = append(out, pgtuner.Advice{
			ID:      "connection.listen-all-without-ssl",
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "Listening on all interfaces without SSL",
			Message: "listen_addresses='*' with ssl=off exposes the PostgreSQL port on every network interface without encryption.",
			Detail:  "Set ssl=on. Configure ssl_cert_file and ssl_key_file. Or restrict listen_addresses to trusted interfaces.",
			DocRef:  docConnRuntime,
		})
	} else if !sslOn {
		out = append(out, pgtuner.Advice{
			ID:      "connection.ssl-disabled",
			Level:   pgtuner.Warn,
			Section: section,
			Title:   "SSL disabled",
			Message: "ssl=off. The server transmits all client traffic (including passwords) in plaintext.",
			Detail:  "Enable SSL to encrypt client-server traffic.",
			DocRef:  docConnRuntime,
		})
	}

	// src: https://www.postgresql.org/docs/current/runtime-config-connection.html#GUC-MAX-CONNECTIONS
	maxConns, hasMaxConns := p.Settings.Int("max_connections")
	if hasMaxConns && maxConns > connPoolerThreshold {
		out = append(out, pgtuner.Advice{
			ID:      "connection.max-connections-high",
			Level:   pgtuner.Info,
			Section: section,
			Title:   "High max_connections; consider a connection pooler",
			Message: fmt.Sprintf("max_connections=%d. Each backend consumes memory and a shared-memory slot. At this count, a pooler (e.g. PgBouncer) is recommended.", maxConns),
			Detail:  "A connection pooler reduces per-backend overhead and improves scalability under bursty workloads.",
			DocRef:  docConnRuntime,
		})
	}

	// pg_settings stores work_mem in kB.
	// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-WORK-MEM
	workMemKB, hasWorkMem := p.Settings.Int("work_mem")
	if hasMaxConns && hasWorkMem {
		workMemBytes := workMemKB * 1024
		worstBytes := maxConns * (workMemBytes + connBaselineBytes)

		// The cgroup-aware limit scales the advice for containers.
		// src: https://docs.kernel.org/admin-guide/cgroup-v2.html (memory.max)
		// src: https://www.postgresql.org/docs/current/runtime-config-resource.html#GUC-WORK-MEM
		effMem, _, err := p.OS.EffectiveMemoryBytes()
		switch {
		case err == nil:
			if effMem > 0 && float64(worstBytes) > ramExhaustFraction*float64(effMem) {
				out = append(out, pgtuner.Advice{
					ID:      "connection.work-mem-exhausts-ram",
					Level:   pgtuner.Bad,
					Section: section,
					Title:   "work_mem × max_connections can exhaust RAM",
					Message: fmt.Sprintf("Worst-case memory (max_connections=%d × (work_mem=%d kB + 2 MiB baseline)) = %.1f GiB. This exceeds 80%% of available memory (%.1f GiB).", maxConns, workMemKB, bytesToGiB(worstBytes), bytesToGiB(int64(effMem))),
					Detail:  "Reduce work_mem, lower max_connections, or use a connection pooler. Under concurrent sort/hash workloads each backend may allocate work_mem multiple times.",
					DocRef:  docWorkMem,
				})
			}
		case err == osinspect.ErrNoOSFacts:
			out = append(out, pgtuner.Advice{
				ID:      "connection.ram-headroom-unverifiable",
				Level:   pgtuner.Info,
				Section: section,
				Title:   "Cannot verify RAM headroom (no OS facts)",
				Message: fmt.Sprintf("Worst-case backend memory is approximately %.1f GiB (max_connections=%d × (work_mem=%d kB + 2 MiB baseline)). Verify that this fits within available RAM.", bytesToGiB(worstBytes), maxConns, workMemKB),
				DocRef:  docWorkMem,
			})
		}
	}

	if len(out) == 0 {
		out = append(out, pgtuner.Advice{
			ID:      "connection.settings-ok",
			Level:   pgtuner.OK,
			Section: section,
			Title:   "Connection settings look healthy",
			Message: "SSL is enabled, max_connections is moderate, and no memory-exhaustion risk was detected.",
		})
	}
	return out
}

func bytesToGiB(b int64) float64 { return float64(b) / (1024 * 1024 * 1024) }
