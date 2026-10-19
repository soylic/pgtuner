package checks

import "sort"

//go:generate go run ./gen .

// extraRuleIDs are IDs produced outside this package.
var extraRuleIDs = []string{"analysis.database-failed"}

// RuleIDs returns every advice ID pgtuner can emit, sorted. Pass any of them,
// or a pattern such as "connection.*", to -ignore.
func RuleIDs() []string {
	out := make([]string, 0, len(generatedRuleIDs)+len(extraRuleIDs))
	out = append(out, generatedRuleIDs...)
	out = append(out, extraRuleIDs...)
	sort.Strings(out)
	return out
}
