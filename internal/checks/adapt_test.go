package checks

import (
	"strings"
	"testing"

	"github.com/soylic/pgtuner/internal/pg"
	"github.com/soylic/pgtuner/pkg/pgtuner"
)

func adaptFixture() ([]pgtuner.Advice, *pg.Settings) {
	s := pg.NewSettings(map[string]string{"work_mem": "4096", "wal_level": "replica", "fsync": "on"})
	return []pgtuner.Advice{
		{ID: "a.one", Level: pgtuner.Warn, Section: "Memory", Detail: "Increase work_mem to 64MB."},
		{ID: "a.two", Level: pgtuner.Warn, Section: "Replication", Detail: "Drop unused slots with pg_drop_replication_slot."},
		{ID: "a.three", Level: pgtuner.OK, Section: "Memory", Detail: "work_mem is fine."},
		{ID: "a.four", Level: pgtuner.Info, Section: "Topology", Detail: "wal_level is replica."},
		{ID: "a.five", Level: pgtuner.Warn, Section: "Memory"},
	}, s
}

func TestAdaptManaged(t *testing.T) {
	adv, s := adaptFixture()
	got := AdaptToEnvironment(adv, s, pgtuner.Topology{}, pgtuner.Flavor{Name: "rds", Managed: true})
	if !strings.Contains(got[0].Detail, "parameter settings") {
		t.Errorf("a setting change must get the hosted-service hint: %q", got[0].Detail)
	}
	for _, i := range []int{1, 2, 3, 4} {
		if strings.Contains(got[i].Detail, "provider") {
			t.Errorf("advice %d must stay unchanged: %q", i, got[i].Detail)
		}
	}
}

func TestAdaptPatroniAndIdempotent(t *testing.T) {
	adv, s := adaptFixture()
	topo := pgtuner.Topology{Manager: &pgtuner.Manager{Name: "patroni", Confidence: "certain"}}
	got := AdaptToEnvironment(adv, s, topo, pgtuner.Flavor{})
	if !strings.HasSuffix(got[0].Detail, "patronictl edit-config.") {
		t.Errorf("detail = %q", got[0].Detail)
	}
	again := AdaptToEnvironment(got, s, topo, pgtuner.Flavor{})
	if strings.Count(again[0].Detail, "patronictl") != 1 {
		t.Errorf("hint added twice: %q", again[0].Detail)
	}
	adv2, _ := adaptFixture()
	maybe := AdaptToEnvironment(adv2, s, pgtuner.Topology{Manager: &pgtuner.Manager{Name: "patroni", Confidence: "probable"}}, pgtuner.Flavor{})
	if !strings.Contains(maybe[0].Detail, "If Patroni manages") {
		t.Errorf("a guess must be worded as a guess: %q", maybe[0].Detail)
	}
}

func TestAdaptPlainServerUntouched(t *testing.T) {
	adv, s := adaptFixture()
	before := adv[0].Detail
	got := AdaptToEnvironment(adv, s, pgtuner.Topology{Manager: &pgtuner.Manager{Name: "repmgr", Confidence: "certain"}}, pgtuner.Flavor{})
	if got[0].Detail != before {
		t.Errorf("repmgr needs no hint, got %q", got[0].Detail)
	}
}

func TestEvalPatroniCluster(t *testing.T) {
	m := &pgtuner.Manager{Name: "patroni", Confidence: "certain", Paused: true}
	topo := pgtuner.Topology{Manager: m, Members: []pgtuner.Member{
		{Name: "pg1", Role: "replica", State: "running", Active: true},
		{Name: "pg2", Role: "replica", State: "start failed", Active: false},
	}}
	got := evalPatroniCluster(topo)
	ids := map[string]pgtuner.Level{}
	for _, a := range got {
		ids[a.ID] = a.Level
	}
	if ids["topology.patroni-no-leader"] != pgtuner.Bad || ids["topology.patroni-paused"] != pgtuner.Warn || ids["topology.patroni-member-unhealthy"] != pgtuner.Warn {
		t.Errorf("advice = %v", ids)
	}
	topo.Members[0].Role = "leader"
	topo.Manager.Paused = false
	topo.Members[1].Active = true
	if got := evalPatroniCluster(topo); len(got) != 0 {
		t.Errorf("a healthy cluster must give no advice, got %v", got)
	}
	topo.Manager.Confidence = "probable"
	if evalPatroniCluster(topo) != nil {
		t.Error("only a certain match may report members")
	}
	if evalPatroniError("") != nil || evalPatroniError("connection refused") == nil {
		t.Error("evalPatroniError must fire only on an error")
	}
}
