package checks

import (
	"regexp"
	"strings"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

const (
	hintManaged        = " This is a hosted service: change settings in the provider's parameter settings, not with ALTER SYSTEM."
	hintPatroni        = " This cluster uses Patroni: change settings with patronictl edit-config."
	hintPatroniMaybe   = " If Patroni manages this server, use patronictl edit-config instead of editing postgresql.conf."
	settingWordPattern = `[a-z][a-z0-9_]{3,}`
)

var settingWord = regexp.MustCompile(settingWordPattern)

// mentionsSetting reports whether text names a PostgreSQL setting that exists on this server.
func mentionsSetting(text string, s *pg.Settings) bool {
	for _, w := range settingWord.FindAllString(text, -1) {
		if _, ok := s.Get(w); ok {
			return true
		}
	}
	return false
}

// AdaptToEnvironment adds one sentence to the Detail of advice that tells the
// reader to change a setting, when the usual way to change it does not work on
// this server: a hosted service, or a Patroni cluster. It reads the setting names
// from the server itself, so it does not guess from common words.
func AdaptToEnvironment(advice []pgtuner.Advice, s *pg.Settings, t pgtuner.Topology, f pgtuner.Flavor) []pgtuner.Advice {
	var hint string
	switch {
	case f.Managed:
		hint = hintManaged
	case t.Manager != nil && t.Manager.Name == "patroni" && t.Manager.Confidence == "certain":
		hint = hintPatroni
	case t.Manager != nil && t.Manager.Name == "patroni":
		hint = hintPatroniMaybe
	default:
		return advice
	}
	for i := range advice {
		a := &advice[i]
		if a.Section == "Topology" || a.Detail == "" || a.Level == pgtuner.OK || a.Level == pgtuner.Unknown {
			continue
		}
		if strings.HasSuffix(a.Detail, hint) || !mentionsSetting(a.Detail, s) {
			continue
		}
		a.Detail += hint
	}
	return advice
}
