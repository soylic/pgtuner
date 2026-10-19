package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	mod := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	tests := []struct {
		name   string
		ldflag string
		info   *debug.BuildInfo
		want   string
	}{
		{"release build", "0.1.0", mod("(devel)"), "0.1.0"},
		{"ldflag with v", "v1.2.3", nil, "1.2.3"},
		{"prerelease", "0.2.0-rc.1", nil, "0.2.0-rc.1"},
		{"go install", "dev", mod("v0.3.1"), "0.3.1"},
		{"go install prerelease", "dev", mod("v1.0.0-beta.2"), "1.0.0-beta.2"},
		{"local build", "dev", mod("(devel)"), "dev"},
		{"pseudo version is not a release", "dev", mod("v0.1.3-0.20261019193735-d096a29319c4"), "dev"},
		{"dirty tree is not a release", "dev", mod("v0.1.3-0.20261019193735-d096a29319c4+dirty"), "dev"},
		{"dirty tagged tree", "dev", mod("v0.2.0+dirty"), "dev"},
		{"no build info", "dev", nil, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.ldflag, tt.info); got != tt.want {
				t.Errorf("resolveVersion(%q) = %q, want %q", tt.ldflag, got, tt.want)
			}
		})
	}
}

func TestSemverRegex(t *testing.T) {
	good := []string{"0.0.1", "v1.2.3", "10.20.30", "1.0.0-rc.1", "1.0.0-alpha-1", "1.0.0+build.5", "1.0.0-beta+exp.sha.5114f85"}
	bad := []string{"1.2", "01.2.3", "1.2.3.4", "v1.2", "dev", "(devel)", "1.0.0-", "1.0.0-rc..1", ""}
	for _, v := range good {
		if !semver.MatchString(v) {
			t.Errorf("%q should be valid semver", v)
		}
	}
	for _, v := range bad {
		if semver.MatchString(v) {
			t.Errorf("%q should not be valid semver", v)
		}
	}
}

func TestDescribeVersion(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef"},
		{Key: "vcs.modified", Value: "true"},
	}}
	got := describeVersion("0.1.0", info)
	for _, want := range []string{"pgtuner 0.1.0", "commit 0123456-dirty", "go1"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeVersion = %q, missing %q", got, want)
		}
	}
	if strings.Contains(describeVersion("dev", nil), "commit") {
		t.Error("no build info means no commit")
	}
}
