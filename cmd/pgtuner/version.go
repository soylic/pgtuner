package main

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// semver matches a Semantic Versioning 2.0.0 version with an optional leading v.
// src: https://semver.org/#is-there-a-suggested-regular-expression-regex-to-check-a-semver-string
var semver = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// untagged matches the module versions Go stamps on builds that are not a tagged release:
// pseudo-versions such as 0.1.3-0.20261019193735-d096a29319c4, and any +dirty tree.
var untagged = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}|\+dirty$`)

// resolveVersion picks the version to show. A release build sets ldflag. A
// "go install pkg@v1.2.3" build has no ldflag, so the module version applies.
// Anything else is a development build.
func resolveVersion(ldflag string, info *debug.BuildInfo) string {
	if ldflag != "" && ldflag != "dev" {
		return strings.TrimPrefix(ldflag, "v")
	}
	if info != nil && semver.MatchString(info.Main.Version) && !untagged.MatchString(info.Main.Version) {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

// describeVersion formats the -version line: the version, the commit when the
// build recorded one, the Go version, and the platform.
func describeVersion(v string, info *debug.BuildInfo) string {
	parts := []string{}
	if info != nil {
		var rev string
		dirty := false
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if len(rev) > 7 {
			rev = rev[:7]
		}
		if rev != "" {
			if dirty {
				rev += "-dirty"
			}
			parts = append(parts, "commit "+rev)
		}
	}
	parts = append(parts, runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)
	return fmt.Sprintf("pgtuner %s (%s)", v, strings.Join(parts, ", "))
}

func readBuildInfo() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return info
}
