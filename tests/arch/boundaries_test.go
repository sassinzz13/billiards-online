// Package arch enforces the architectural boundaries described in MEMORY.md §5 and ADR 0001.
//
// A layer table that nothing checks is a comment. Import discipline decays under deadline pressure,
// and by the time the decay is visible, untangling it is a rewrite. This test makes the rules
// mechanical: a violating import fails the build with a message naming the offending edge.
//
// It reads the real import graph from `go list -json ./...`, so it cannot drift from what the
// compiler actually sees. No third-party linter is needed for something this small (§9).
//
// If a change requires editing this file, that is the signal to stop and ask whether the new
// dependency is correct — not to edit the rule and move on. If the layer table genuinely needs to
// change, update ADR 0001 and MEMORY.md with the reason.
package arch

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const module = "github.com/sassinzz13/billiards-online"

// featureLayers assigns every product feature to a layer. Imports may only point strictly
// downward: a feature may import a lower layer, never a higher one and never its own.
//
// Forbidding same-layer imports is deliberate. Two features on one layer are declared independent,
// so an import between them is either a mistake or a sign that one belongs on a higher layer.
var featureLayers = map[string]int{
	"users":        0,
	"wallet":       0,
	"auth":         1,
	"leaderboards": 1,
	"wagering":     2,
	"matches":      3,
	"rooms":        4,
	"lobby":        5,
	"matchmaking":  5,
	"realtime":     6,
}

// pkg is the subset of `go list -json` output this test needs.
type pkg struct {
	ImportPath  string
	Imports     []string
	Deps        []string
	TestImports []string
}

var (
	loadOnce sync.Once
	packages []pkg
	loadErr  error
)

// load runs `go list -json ./...` from the module root.
func load(t *testing.T) []pkg {
	t.Helper()
	loadOnce.Do(func() {
		root, err := filepath.Abs("../..")
		if err != nil {
			loadErr = err
			return
		}

		cmd := exec.Command("go", "list", "-json", "./...")
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			loadErr = err
			return
		}

		dec := json.NewDecoder(strings.NewReader(string(out)))
		for dec.More() {
			var p pkg
			if err := dec.Decode(&p); err != nil {
				loadErr = err
				return
			}
			packages = append(packages, p)
		}
	})
	if loadErr != nil {
		t.Fatalf("load package graph: %v", loadErr)
	}
	return packages
}

// TestFeatureLayering enforces the layer table: feature imports point strictly downward.
//
// This is what makes circular feature dependencies structurally impossible rather than merely
// discouraged. Go rejects an actual import cycle, but a dependency graph degrades into mutual
// dependency long before that.
func TestFeatureLayering(t *testing.T) {
	for _, p := range load(t) {
		from, ok := featureOf(p.ImportPath)
		if !ok {
			continue
		}
		fromLayer, known := featureLayers[from]
		if !known {
			t.Errorf("feature %q is not in the layer table — add it to featureLayers and MEMORY.md §5", from)
			continue
		}

		for _, imp := range p.Imports {
			to, ok := featureOf(imp)
			if !ok || to == from {
				continue
			}
			toLayer, known := featureLayers[to]
			if !known {
				t.Errorf("%s imports unknown feature %q — add it to the layer table", p.ImportPath, to)
				continue
			}

			if toLayer >= fromLayer {
				t.Errorf(
					"layer violation: %s (%s, L%d) imports %s (%s, L%d).\n"+
						"  Imports must point strictly downward. Either invert the dependency using a\n"+
						"  consumer-side interface (see ADR 0001), or reconsider the layer assignment.",
					p.ImportPath, from, fromLayer, imp, to, toLayer,
				)
			}
		}
	}
}

// TestGameEngineIsStdlibOnly enforces that the billiards engine imports nothing but the standard
// library.
//
// This is what keeps `go test ./game/...` runnable with no database, no Docker, and no network
// (§15), and what would make the engine extractable later. Deps rather than Imports is checked
// deliberately: a transitive dependency on pgx is just as disqualifying as a direct one.
func TestGameEngineIsStdlibOnly(t *testing.T) {
	for _, p := range load(t) {
		if !strings.HasPrefix(p.ImportPath, module+"/game/") && p.ImportPath != module+"/game" {
			continue
		}
		for _, dep := range p.Deps {
			if isStdlib(dep) || strings.HasPrefix(dep, module+"/game") {
				continue
			}
			t.Errorf(
				"%s depends on %s.\n"+
					"  game/** must import the standard library only — no Gin, no pgx, no WebSockets,\n"+
					"  no internal/**, no platform/**. See ADR 0004 and MEMORY.md §6.",
				p.ImportPath, dep,
			)
		}
	}
}

// TestPlatformHasNoProductDependencies enforces that platform packages provide technical
// capability and nothing else (§7).
//
// platform/postgres may hold pool setup and a transaction helper. It may never hold CreateRoom or
// SettleWager. The import direction is the mechanical proxy for that rule.
func TestPlatformHasNoProductDependencies(t *testing.T) {
	for _, p := range load(t) {
		if !strings.HasPrefix(p.ImportPath, module+"/platform/") {
			continue
		}
		for _, dep := range p.Deps {
			switch {
			case strings.HasPrefix(dep, module+"/internal/"):
				t.Errorf("%s depends on %s — platform must not depend on product features (§7)",
					p.ImportPath, dep)
			case strings.HasPrefix(dep, module+"/game"):
				t.Errorf("%s depends on %s — platform must not depend on the game engine (§7)",
					p.ImportPath, dep)
			}
		}
	}
}

// TestNothingImportsTheCompositionRoot enforces that apps/** is a leaf.
//
// The composition root wires features together. Anything importing it would invert that
// relationship and create a path for features to reach each other implicitly.
func TestNothingImportsTheCompositionRoot(t *testing.T) {
	for _, p := range load(t) {
		if strings.HasPrefix(p.ImportPath, module+"/apps/") {
			continue
		}
		for _, dep := range append(append([]string{}, p.Deps...), p.TestImports...) {
			if strings.HasPrefix(dep, module+"/apps/") {
				t.Errorf("%s depends on %s — nothing may import the composition root", p.ImportPath, dep)
			}
		}
	}
}

// TestNoDumpingGroundPackages enforces §6.
//
// A package named shared, common, util, or helpers has no owner, so everything drifts into it.
// Small duplication is better than the wrong abstraction; when a third occurrence proves an
// abstraction is real, it gets a name that says what it does.
func TestNoDumpingGroundPackages(t *testing.T) {
	banned := map[string]bool{
		"shared": true, "common": true, "util": true, "utils": true,
		"helper": true, "helpers": true, "misc": true, "core": true,
	}
	for _, p := range load(t) {
		if !strings.HasPrefix(p.ImportPath, module+"/") {
			continue
		}
		for _, segment := range strings.Split(strings.TrimPrefix(p.ImportPath, module+"/"), "/") {
			if banned[segment] {
				t.Errorf(
					"package %s uses the banned path segment %q.\n"+
						"  Name packages for what they own, not for the fact that two callers exist (§6).",
					p.ImportPath, segment,
				)
			}
		}
	}
}

// TestFeaturesDoNotImportEachOthersInternals is a placeholder guard that becomes meaningful once
// features exist. A feature is entered through its own package; reaching into a subpackage of
// another feature bypasses whatever boundary that feature intended.
func TestFeaturesDoNotImportEachOthersInternals(t *testing.T) {
	for _, p := range load(t) {
		from, ok := featureOf(p.ImportPath)
		if !ok {
			continue
		}
		for _, imp := range p.Imports {
			to, ok := featureOf(imp)
			if !ok || to == from {
				continue
			}
			// internal/<feature> is the entry point; internal/<feature>/anything is not.
			if imp != module+"/internal/"+to {
				t.Errorf(
					"%s imports %s — reach another feature only through its top-level package, "+
						"never a subpackage",
					p.ImportPath, imp,
				)
			}
		}
	}
}

// featureOf returns the feature owning an import path, if it is a feature package at all.
func featureOf(importPath string) (string, bool) {
	const prefix = module + "/internal/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(importPath, prefix)
	name, _, _ := strings.Cut(rest, "/")
	if name == "" {
		return "", false
	}
	return name, true
}

// isStdlib reports whether an import path belongs to the standard library.
//
// Standard library paths have no dot in their first segment: "net/http" is stdlib,
// "github.com/gin-gonic/gin" is not.
func isStdlib(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}
