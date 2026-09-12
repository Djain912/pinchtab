package extract

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pinchtab/pinchtab/internal/bridge/observe"
	"github.com/pinchtab/pinchtab/internal/semdesc"
	"github.com/pinchtab/semantic"
)

// Reasons a field is left missing rather than filled with a wrong-typed value.
const (
	reasonNoMatch     = "no_match"
	reasonNotNumeric  = "not_numeric"
	reasonNotBoolean  = "not_boolean"
	reasonUnsupported = "unsupported"
	reasonRefNotFound = "ref_not_found"
)

// Field value sources, in the order they are tried.
const (
	sourceValue   = "value"
	sourceText    = "text"
	sourceName    = "name"
	sourceChecked = "checked"
)

// Options tunes resolution. Zero values fall back to the same defaults /find
// uses (threshold 0.3, the shared combined matcher).
type Options struct {
	Threshold       float64
	LexicalWeight   float64
	EmbeddingWeight float64
	// Matcher overrides the default combined matcher; nil uses the shared one.
	Matcher semantic.ElementMatcher
}

// FieldResult is the per-field outcome. Ref/Score/Confidence let an agent fall
// back to /find on a low-confidence field; Reason explains a missing field.
type FieldResult struct {
	Ref        string  `json:"ref,omitempty"`
	Score      float64 `json:"score"`
	Confidence string  `json:"confidence"`
	Source     string  `json:"source,omitempty"`
	Reason     string  `json:"reason,omitempty"`
}

// Result is the extraction outcome: typed data, per-field diagnostics, and the
// required fields that could not be filled (never invented).
type Result struct {
	Data    map[string]any         `json:"data"`
	Fields  map[string]FieldResult `json:"fields"`
	Missing []string               `json:"missing"`
}

var (
	defaultMatcherOnce sync.Once
	defaultMatcher     semantic.ElementMatcher
)

func sharedMatcher() semantic.ElementMatcher {
	defaultMatcherOnce.Do(func() {
		defaultMatcher = semantic.NewCombinedMatcher(semantic.NewHashingEmbedder(128))
	})
	return defaultMatcher
}

// Resolve fills each schema property against the node list. It is deterministic:
// nodes are canonicalised to document order (by ref) before matching, so the
// same schema over the same nodes — in any input order — yields identical output.
func Resolve(schema Schema, nodes []observe.A11yNode, opts Options) Result {
	if opts.Threshold <= 0 {
		opts.Threshold = 0.3
	}
	matcher := opts.Matcher
	if matcher == nil {
		matcher = sharedMatcher()
	}

	ordered := canonicalOrder(nodes)
	byRef := make(map[string]observe.A11yNode, len(ordered))
	for _, n := range ordered {
		byRef[n.Ref] = n
	}
	descs := semdesc.Build(ordered)

	result := Result{
		Data:   map[string]any{},
		Fields: map[string]FieldResult{},
	}
	for _, prop := range schema.Properties {
		fr, value, ok := resolveField(prop, descs, byRef, matcher, opts)
		result.Fields[prop.Name] = fr
		if ok {
			result.Data[prop.Name] = value
		} else if prop.Required {
			result.Missing = append(result.Missing, prop.Name)
		}
	}
	sort.Strings(result.Missing)
	return result
}

func resolveField(prop Property, descs []semantic.ElementDescriptor, byRef map[string]observe.A11yNode, matcher semantic.ElementMatcher, opts Options) (FieldResult, any, bool) {
	if prop.Type == TypeArray {
		return FieldResult{Confidence: semantic.CalibrateConfidence(0), Reason: reasonUnsupported}, nil, false
	}

	node, fr, ok := matchNode(prop, descs, byRef, matcher, opts)
	if !ok {
		return fr, nil, false
	}

	value, source, ok := readValue(prop.Type, node)
	fr.Source = source
	if !ok {
		fr.Reason = coercionReason(prop.Type)
		return fr, nil, false
	}
	return fr, value, true
}

// matchNode finds the best node for a property: a ref hint selects verbatim,
// otherwise the matcher scores the name+description(+hint) query.
func matchNode(prop Property, descs []semantic.ElementDescriptor, byRef map[string]observe.A11yNode, matcher semantic.ElementMatcher, opts Options) (observe.A11yNode, FieldResult, bool) {
	if prop.hintKindResolved == hintRef {
		node, found := byRef[prop.hintValue]
		fr := FieldResult{Ref: prop.hintValue, Score: 1, Confidence: semantic.CalibrateConfidence(1), Source: "hint"}
		if !found {
			fr.Score = 0
			fr.Confidence = semantic.CalibrateConfidence(0)
			fr.Reason = reasonRefNotFound
			return observe.A11yNode{}, fr, false
		}
		return node, fr, true
	}

	// TopK spans every descriptor and the winner is chosen here, not taken from
	// res.BestRef: the combined matcher merges its lexical and embedding halves
	// concurrently, so its tie ordering is not stable. Per-ref scores are, so a
	// deterministic pick (score desc, then document order) makes Resolve stable.
	res, err := matcher.Find(context.Background(), fieldQuery(prop), descs, semantic.FindOptions{
		Threshold:       opts.Threshold,
		TopK:            len(descs),
		LexicalWeight:   opts.LexicalWeight,
		EmbeddingWeight: opts.EmbeddingWeight,
	})
	best, ok := pickBest(res.Matches)
	score := roundScore(best.Score)
	fr := FieldResult{Score: score, Confidence: semantic.CalibrateConfidence(score)}
	if err != nil || !ok {
		fr.Reason = reasonNoMatch
		return observe.A11yNode{}, fr, false
	}
	fr.Ref = best.Ref
	node, found := byRef[best.Ref]
	if !found {
		fr.Reason = reasonNoMatch
		return observe.A11yNode{}, fr, false
	}
	return node, fr, true
}

// pickBest chooses the winning match deterministically: highest score, ties
// broken by document order (numeric ref ascending). Scores are compared rounded
// because the combined matcher's concurrent merge is only stable to ~1 ULP, so
// two matches within rounding are a tie the ref order settles. ok is false when
// empty.
func pickBest(matches []semantic.ElementMatch) (semantic.ElementMatch, bool) {
	best, ok := semantic.ElementMatch{}, false
	var bestScore float64
	for _, m := range matches {
		s := roundScore(m.Score)
		switch {
		case !ok, s > bestScore, s == bestScore && refLess(m.Ref, best.Ref):
			best, bestScore, ok = m, s, true
		}
	}
	return best, ok
}

// roundScore quantises a heuristic score to 6 decimals so equal-in-practice
// scores compare equal across runs despite the matcher's 1-ULP jitter.
func roundScore(s float64) float64 {
	return math.Round(s*1e6) / 1e6
}

// fieldQuery builds the matcher query. A resolved hint query wins over the
// name-based query; otherwise name and description are combined.
func fieldQuery(prop Property) string {
	if prop.hintKindResolved == hintQuery {
		return prop.hintValue
	}
	return strings.TrimSpace(prop.Name + " " + prop.Description)
}

// readValue reads the node's value for a type and coerces it. Priority is
// Value, Text, Name; booleans consult Checked first. A coercion failure returns
// ok=false so the field is left missing rather than wrong-typed.
func readValue(t Type, node observe.A11yNode) (any, string, bool) {
	if t == TypeBoolean {
		if node.Checked == observe.CheckedTrue || node.Checked == observe.CheckedFalse || node.Checked == observe.CheckedMixed {
			if b, ok := coerceBool(node, ""); ok {
				return b, sourceChecked, true
			}
			return nil, sourceChecked, false
		}
	}

	raw, source := rawValue(node)
	if raw == "" {
		return nil, "", false
	}

	switch t {
	case TypeString:
		return strings.TrimSpace(raw), source, true
	case TypeNumber:
		if f, ok := coerceNumber(raw); ok {
			return f, source, true
		}
		return nil, source, false
	case TypeInteger:
		if f, ok := coerceNumber(raw); ok {
			return int64(f), source, true
		}
		return nil, source, false
	case TypeBoolean:
		if b, ok := coerceBool(node, raw); ok {
			return b, source, true
		}
		return nil, source, false
	}
	return nil, source, false
}

func rawValue(node observe.A11yNode) (string, string) {
	switch {
	case strings.TrimSpace(node.Value) != "":
		return node.Value, sourceValue
	case strings.TrimSpace(node.Text) != "":
		return node.Text, sourceText
	case strings.TrimSpace(node.Name) != "":
		return node.Name, sourceName
	}
	return "", ""
}

func coercionReason(t Type) string {
	switch t {
	case TypeNumber, TypeInteger:
		return reasonNotNumeric
	case TypeBoolean:
		return reasonNotBoolean
	default:
		return reasonNoMatch
	}
}

// canonicalOrder returns a copy of nodes sorted into document order by ref, so
// resolution is invariant to input node order. Snapshot refs ("e5", "e12") are
// assigned in pre-order, so their numeric order is document order.
func canonicalOrder(nodes []observe.A11yNode) []observe.A11yNode {
	ordered := make([]observe.A11yNode, len(nodes))
	copy(ordered, nodes)
	sort.SliceStable(ordered, func(i, j int) bool {
		return refLess(ordered[i].Ref, ordered[j].Ref)
	})
	return ordered
}

func refLess(a, b string) bool {
	na, oka := refNum(a)
	nb, okb := refNum(b)
	if oka && okb {
		return na < nb
	}
	if oka != okb {
		return oka // numeric refs sort before non-numeric ones
	}
	return a < b
}

func refNum(ref string) (int, bool) {
	if len(ref) < 2 || ref[0] != 'e' {
		return 0, false
	}
	n, err := strconv.Atoi(ref[1:])
	if err != nil {
		return 0, false
	}
	return n, true
}
