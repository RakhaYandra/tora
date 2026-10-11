package orchestrator

import (
	"reflect"
	"testing"
)

func deps() map[string][]string {
	return map[string][]string{
		"web":      {"api"},
		"api":      {"postgres", "redis"},
		"worker":   {"postgres", "redis"},
		"postgres": {},
		"redis":    {},
	}
}

func TestSortLevels(t *testing.T) {
	levels, err := Sort(deps())
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"postgres", "redis"}, {"api", "worker"}, {"web"}}
	if !reflect.DeepEqual(levels, want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
}

func TestShutdownOrder(t *testing.T) {
	levels, _ := Sort(deps())
	got := ShutdownOrder(levels)
	want := []string{"web", "api", "worker", "postgres", "redis"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown = %v, want %v", got, want)
	}
}

func TestCycle(t *testing.T) {
	m := map[string][]string{"a": {"b"}, "b": {"a"}}
	if _, err := Sort(m); err == nil {
		t.Fatal("expected cycle error")
	}
	m = map[string][]string{"a": {"a"}}
	if _, err := Sort(m); err == nil {
		t.Fatal("expected self-cycle error")
	}
}

func TestUnknownDep(t *testing.T) {
	m := map[string][]string{"a": {"ghost"}}
	if _, err := Sort(m); err == nil {
		t.Fatal("expected unknown dependency error")
	}
}

func TestSubgraph(t *testing.T) {
	sub, err := Subgraph(deps(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 4 {
		t.Fatalf("subgraph size = %d, want 4", len(sub))
	}
	if _, ok := sub["worker"]; ok {
		t.Fatal("worker must not be in web subgraph")
	}
	if _, err := Subgraph(deps(), "ghost"); err == nil {
		t.Fatal("expected unknown service error")
	}
}

func TestDependents(t *testing.T) {
	got := Dependents(deps(), "postgres")
	want := []string{"api", "worker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dependents = %v, want %v", got, want)
	}
}
