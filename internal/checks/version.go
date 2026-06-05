package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/OpenRemedy/pgtuner/pkg/pgtuner"
)

// docVersioning is the reference for community EOL dates.
// src: https://www.postgresql.org/support/versioning/
const docVersioning = "https://www.postgresql.org/support/versioning/"

// eolDates maps PostgreSQL major version to the community end-of-life date.
// Dates are the final community EOL as published at postgresql.org/support/versioning/
// (typically the second Thursday of November in the 5th year after release).
// src: https://www.postgresql.org/support/versioning/
var eolDates = map[int]time.Time{
	13: time.Date(2025, 11, 13, 0, 0, 0, 0, time.UTC),
	14: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC),
	15: time.Date(2027, 11, 11, 0, 0, 0, 0, time.UTC),
	16: time.Date(2028, 11, 9, 0, 0, 0, 0, time.UTC),
	17: time.Date(2029, 11, 8, 0, 0, 0, 0, time.UTC),
	18: time.Date(2030, 11, 14, 0, 0, 0, 0, time.UTC),
}

// eolWarnWindow is how far before the EOL date to start warning.
// 12 months gives operators a planning horizon for upgrades.
const eolWarnWindow = 365 * 24 * time.Hour

// VersionCheck checks whether the running PostgreSQL major version is within
// its community support window.
type VersionCheck struct{ clusterScoped }

func (VersionCheck) Name() string { return "version" }

// Run checks the server major version against known community EOL dates.
// It never panics and never returns an error.
func (VersionCheck) Run(_ context.Context, p *Probe) []pgtuner.Advice {
	const section = "Version"
	v := p.Version
	now := p.now()

	eol, known := eolDates[v.Major]
	if !known {
		return []pgtuner.Advice{{
			Level:   pgtuner.Info,
			Section: section,
			Title:   fmt.Sprintf("EOL date unknown for PostgreSQL %d", v.Major),
			Message: fmt.Sprintf("Running PostgreSQL %s; no community EOL date is on record for major version %d. Check %s for the latest schedule.", v, v.Major, docVersioning),
			DocRef:  docVersioning,
		}}
	}

	switch {
	case now.After(eol):
		return []pgtuner.Advice{{
			Level:   pgtuner.Bad,
			Section: section,
			Title:   fmt.Sprintf("PostgreSQL %d is end-of-life", v.Major),
			Message: fmt.Sprintf("Running PostgreSQL %s; community support ended on %s. This version no longer receives security fixes. Upgrade immediately.", v, eol.Format("2006-01-02")),
			DocRef:  docVersioning,
		}}

	case now.Add(eolWarnWindow).After(eol):
		// Within 12 months of EOL.
		daysLeft := int(eol.Sub(now).Hours() / 24)
		return []pgtuner.Advice{{
			Level:   pgtuner.Warn,
			Section: section,
			Title:   fmt.Sprintf("PostgreSQL %d reaches end-of-life in %d days", v.Major, daysLeft),
			Message: fmt.Sprintf("Running PostgreSQL %s; community support ends on %s (%d days away). Plan your upgrade.", v, eol.Format("2006-01-02"), daysLeft),
			DocRef:  docVersioning,
		}}

	default:
		return []pgtuner.Advice{{
			Level:   pgtuner.OK,
			Section: section,
			Title:   fmt.Sprintf("PostgreSQL %d is supported", v.Major),
			Message: fmt.Sprintf("Running PostgreSQL %s; community support continues until %s.", v, eol.Format("2006-01-02")),
			DocRef:  docVersioning,
		}}
	}
}
