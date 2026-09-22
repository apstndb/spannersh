package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestCIPatchLaneConditionsRun checks that the pinned CI Go patch, the
// conditions that gate patch-only steps, and the documented minimum stay on
// the same version. It evaluates those conditions for every matrix lane.
// Parsing the workflow as text is not enough: a stale equality check would
// still be valid YAML while skipping go generate on the patch lane.
func TestCIPatchLaneConditionsRun(t *testing.T) {
	goVer := readGoModVersion(t)
	ci := string(mustReadRepoFile(t, ".github/workflows/ci.yml"))
	matrix := ciMatrixGoVersions(t, ci)
	if len(matrix) != 2 || matrix[0] != goVer || matrix[1] != "stable" {
		t.Fatalf("CI matrix = %q, want [%s stable]", matrix, goVer)
	}

	steps := ciSteps(t, ci)
	gated := map[string]string{}
	for _, step := range steps {
		if step.ifExpr == "" {
			continue
		}
		ver, ok := matrixGoEquals(step.ifExpr)
		if !ok {
			t.Fatalf("step %q has unsupported condition %q", step.label, step.ifExpr)
		}
		if ver != goVer {
			t.Fatalf("step %q condition pins %s, go.mod pins %s", step.label, ver, goVer)
		}
		gated[step.label] = step.ifExpr
	}

	for _, label := range []string{"jdx/mise-action@v4", "Verify generated docs"} {
		expr, ok := gated[label]
		if !ok {
			t.Fatalf("step %q is not gated on the go.mod patch lane", label)
		}
		for _, lane := range matrix {
			runs := evalMatrixGoEquals(t, expr, lane)
			want := lane == goVer
			if runs != want {
				t.Fatalf("lane %s step %q runs=%v, want %v", lane, label, runs, want)
			}
		}
	}
	if len(gated) != 2 {
		t.Fatalf("gated steps = %v, want only the mise and generated-doc steps", gated)
	}

	readme := string(mustReadRepoFile(t, "README.md"))
	if !strings.Contains(readme, "Go "+goVer+"+") {
		t.Fatalf("README prerequisite does not require Go %s+", goVer)
	}
	for _, path := range []string{".github/workflows/security.yml", ".github/workflows/release.yml"} {
		body := string(mustReadRepoFile(t, path))
		if !strings.Contains(body, "go-version-file: go.mod") {
			t.Fatalf("%s does not select Go from go.mod", path)
		}
		if strings.Contains(body, "go-version:") {
			t.Fatalf("%s hardcodes go-version instead of tracking go.mod", path)
		}
	}
}

func readGoModVersion(t *testing.T) string {
	t.Helper()
	for _, line := range strings.Split(string(mustReadRepoFile(t, "go.mod")), "\n") {
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	t.Fatal("go.mod has no go version")
	return ""
}

func ciMatrixGoVersions(t *testing.T, workflow string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^[ \t]+go:\s+\[(.*)\]\s*$`)
	m := re.FindStringSubmatch(workflow)
	if m == nil {
		t.Fatal("CI workflow has no go matrix")
	}
	quoted := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1)
	if len(quoted) == 0 {
		t.Fatalf("CI go matrix is empty: %s", m[1])
	}
	out := make([]string, 0, len(quoted))
	for _, q := range quoted {
		out = append(out, q[1])
	}
	return out
}

type ciStep struct {
	label  string
	ifExpr string
}

func ciSteps(t *testing.T, workflow string) []ciStep {
	t.Helper()
	var steps []ciStep
	var cur *ciStep
	inSteps := false
	for _, line := range strings.Split(workflow, "\n") {
		if !inSteps {
			if strings.TrimSpace(line) == "steps:" {
				inSteps = true
			}
			continue
		}
		if strings.HasPrefix(line, "      - ") {
			if cur != nil {
				steps = append(steps, *cur)
			}
			cur = &ciStep{label: stepLabel(strings.TrimPrefix(line, "      - "))}
			continue
		}
		if cur == nil {
			continue
		}
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "name:") && cur.label == "":
			cur.label = strings.TrimSpace(strings.TrimPrefix(trim, "name:"))
		case strings.HasPrefix(trim, "uses:") && cur.label == "":
			cur.label = strings.TrimSpace(strings.TrimPrefix(trim, "uses:"))
		case strings.HasPrefix(trim, "if:"):
			cur.ifExpr = strings.TrimSpace(strings.TrimPrefix(trim, "if:"))
		}
	}
	if cur != nil {
		steps = append(steps, *cur)
	}
	if len(steps) == 0 {
		t.Fatal("CI workflow has no steps")
	}
	return steps
}

func stepLabel(rest string) string {
	switch {
	case strings.HasPrefix(rest, "name:"):
		return strings.TrimSpace(strings.TrimPrefix(rest, "name:"))
	case strings.HasPrefix(rest, "uses:"):
		return strings.TrimSpace(strings.TrimPrefix(rest, "uses:"))
	default:
		return strings.TrimSpace(rest)
	}
}

func matrixGoEquals(expr string) (string, bool) {
	m := regexp.MustCompile(`^matrix\.go == '([^']+)'$`).FindStringSubmatch(strings.TrimSpace(expr))
	if m == nil {
		return "", false
	}
	return m[1], true
}

func evalMatrixGoEquals(t *testing.T, expr, matrixGo string) bool {
	t.Helper()
	ver, ok := matrixGoEquals(expr)
	if !ok {
		t.Fatalf("cannot evaluate condition %q", expr)
	}
	return ver == matrixGo
}

func mustReadRepoFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}
