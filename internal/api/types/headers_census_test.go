package types

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pinchtab/pinchtab/internal/srccensus"
)

const (
	moduleRoot     = "../../.."
	minModuleFiles = 300
	headerHomeFile = "internal/api/types/headers.go"
)

// exemptHeaders are the X-PinchTab-* names that legitimately live outside the wire-header
// home. Each is already single-homed and belongs to a different contract, so the census
// allows its one spelling to sit in the named file rather than in api/types. A new wire
// header must go in headers.go instead of being added here.
var exemptHeaders = map[string]string{
	"X-PinchTab-Internal-Token": "internal/handlers/trust.go: server-only trusted-hop token, never part of the CLI/MCP wire contract",
	"X-PinchTab-Event":          "internal/scheduler/webhook.go: outbound event-webhook contract, separate from the wire headers",
	"X-PinchTab-Task-ID":        "internal/scheduler/webhook.go: outbound event-webhook contract, separate from the wire headers",
	"X-PinchTab-Session-Id":     "internal/activity/context.go: activity-only identity header, already single-homed",
	"X-PinchTab-Instance-Id":    "internal/activity/context.go: activity-only identity header, already single-homed",
	"X-PinchTab-Profile-Id":     "internal/activity/context.go: activity-only identity header, already single-homed",
	"X-PinchTab-Profile-Name":   "internal/activity/context.go: activity-only identity header, already single-homed",
	"X-PinchTab-Tab-Created":    "internal/activity/context.go: activity-only identity header, already single-homed",
}

// pinchtabHeaderLiterals maps each X-PinchTab-* string literal to the module-relative files
// it is spelled in. It reads AST string literals only, so a comment naming a header (this
// package's own doc comment does) is not counted.
func pinchtabHeaderLiterals(t *testing.T, files []srccensus.SourceFile) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, f := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), f.Name, f.Text, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f.Name, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(lit.Value)
			if err == nil && strings.HasPrefix(val, "X-PinchTab-") {
				out[val] = append(out[val], f.Name)
			}
			return true
		})
	}
	return out
}

func singleHomeViolations(headers map[string][]string) []string {
	var violations []string
	for header, locs := range headers {
		if len(locs) != 1 {
			sort.Strings(locs)
			violations = append(violations, fmt.Sprintf("%s is spelled in %d places, want exactly one so a one-character drift is a compile error, not a silent no-op: %v", header, len(locs), locs))
			continue
		}
		if _, exempt := exemptHeaders[header]; exempt {
			if locs[0] == headerHomeFile {
				violations = append(violations, fmt.Sprintf("%s is listed as exempt but sits in the wire-header home %s", header, headerHomeFile))
			}
			continue
		}
		if locs[0] != headerHomeFile {
			violations = append(violations, fmt.Sprintf("%s is a wire header spelled in %s; move it to %s, or if it is not a wire header add it to exemptHeaders with a reason", header, locs[0], headerHomeFile))
		}
	}
	sort.Strings(violations)
	return violations
}

func TestEachWireHeaderLiteralIsSingleHomed(t *testing.T) {
	headers := pinchtabHeaderLiterals(t, srccensus.Tree(t, moduleRoot, minModuleFiles))

	for _, wire := range []string{HeaderVocab, HeaderTabID, HeaderSource} {
		locs := headers[wire]
		if len(locs) != 1 || locs[0] != headerHomeFile {
			t.Errorf("wire header %s must be spelled exactly once, in %s; found %v", wire, headerHomeFile, locs)
		}
	}

	if v := singleHomeViolations(headers); len(v) > 0 {
		t.Errorf("X-PinchTab-* header names are not single-homed:\n%s", strings.Join(v, "\n"))
	}
}

// The guard bites: a second spelling of a wire header is reported as a violation.
func TestCensusFlagsADuplicateSpelling(t *testing.T) {
	planted := []srccensus.SourceFile{
		{Name: headerHomeFile, Text: "package types\nconst A = \"X-PinchTab-Vocab\"\n"},
		{Name: "internal/somewhere/dup.go", Text: "package somewhere\nconst B = \"X-PinchTab-Vocab\"\n"},
	}
	headers := pinchtabHeaderLiterals(t, planted)
	if len(headers["X-PinchTab-Vocab"]) != 2 {
		t.Fatalf("the AST census did not see the planted duplicate: %v", headers["X-PinchTab-Vocab"])
	}
	if len(singleHomeViolations(headers)) == 0 {
		t.Fatal("a duplicated wire-header spelling passed the census; a typo on one side would silently disable the feature")
	}
}

// A comment that names a header is not a spelling: the AST walk sees literals only.
func TestCensusIgnoresHeadersNamedInComments(t *testing.T) {
	commentOnly := []srccensus.SourceFile{
		{Name: "internal/foo/foo.go", Text: "package foo\n// mentions X-PinchTab-Vocab in prose only\nconst X = 1\n"},
	}
	if got := pinchtabHeaderLiterals(t, commentOnly)["X-PinchTab-Vocab"]; len(got) != 0 {
		t.Fatalf("a header named only in a comment was counted as a spelling: %v", got)
	}
}
