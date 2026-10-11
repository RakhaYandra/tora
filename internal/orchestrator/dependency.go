// Package orchestrator orders services by dependency and drives their lifecycle.
package orchestrator

import (
	"fmt"
	"sort"
)

// Sort returns startup levels: services within a level are independent and may
// start in parallel. The reverse order is the shutdown order.
// Unknown dependencies and cycles are errors.
func Sort(services map[string][]string) ([][]string, error) {
	for svc, deps := range services {
		for _, d := range deps {
			if _, ok := services[d]; !ok {
				return nil, fmt.Errorf("unknown dependency %q of service %q", d, svc)
			}
		}
	}
	indeg := make(map[string]int, len(services))
	rdeps := make(map[string][]string, len(services))
	for svc, deps := range services {
		indeg[svc] += 0
		for _, d := range deps {
			indeg[svc]++
			rdeps[d] = append(rdeps[d], svc)
		}
	}
	var levels [][]string
	done := map[string]bool{}
	for len(done) < len(services) {
		var lvl []string
		for svc, n := range indeg {
			if n == 0 && !done[svc] {
				lvl = append(lvl, svc)
			}
		}
		if len(lvl) == 0 {
			return nil, fmt.Errorf("dependency cycle detected among services")
		}
		sort.Strings(lvl)
		levels = append(levels, lvl)
		for _, svc := range lvl {
			done[svc] = true
			for _, r := range rdeps[svc] {
				indeg[r]--
			}
		}
	}
	return levels, nil
}

// ShutdownOrder flattens levels in reverse: dependents stop before dependencies.
func ShutdownOrder(levels [][]string) []string {
	var out []string
	for i := len(levels) - 1; i >= 0; i-- {
		out = append(out, levels[i]...)
	}
	return out
}

// Subgraph returns the transitive closure of target plus its dependencies.
func Subgraph(services map[string][]string, target string) (map[string][]string, error) {
	if _, ok := services[target]; !ok {
		return nil, fmt.Errorf("unknown service %q", target)
	}
	sub := map[string][]string{}
	var visit func(string)
	visit = func(s string) {
		if _, ok := sub[s]; ok {
			return
		}
		sub[s] = services[s]
		for _, d := range services[s] {
			visit(d)
		}
	}
	visit(target)
	return sub, nil
}

// Dependents returns services that directly depend on target.
func Dependents(services map[string][]string, target string) []string {
	var out []string
	for svc, deps := range services {
		for _, d := range deps {
			if d == target {
				out = append(out, svc)
			}
		}
	}
	sort.Strings(out)
	return out
}
