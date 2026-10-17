package class

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// TestSpansCoverLayout checks that the class table and save.Layout cover
// the same offsets: Spans is save.Layout span for span (offset, length and
// field), and every rule is keyed to exactly one Layout span.
func TestSpansCoverLayout(t *testing.T) {
	spans, layout := Spans(), save.Layout()
	if len(spans) != len(layout) {
		t.Fatalf("%d class spans, %d save.Layout spans", len(spans), len(layout))
	}
	for i, l := range layout {
		if s := spans[i]; s.Off != l.Off || s.Len != l.Len || s.Field != l.Field {
			t.Errorf("span %d: class 0x%03X+%d %q, save.Layout 0x%03X+%d %q", i, s.Off, s.Len, s.Field, l.Off, l.Len, l.Field)
		}
	}
	if len(rules) != len(layout) {
		t.Errorf("%d rules for %d save.Layout spans", len(rules), len(layout))
	}
	if err := validateRules(rules); err != nil {
		t.Error(err)
	}
}

// TestValidateRulesCatchesStrayKeys proves the rule check can fail: a rule
// keyed between Layout spans, or twice to one, is reported, and a span
// keyed twice builds as UNASSIGNED.
func TestValidateRulesCatchesStrayKeys(t *testing.T) {
	l := save.Layout()
	stray := append(append([]keyed(nil), rules...), keyed{Off: l[0].Off + 1, Rule: Rule{Class: Never}})
	if err := validateRules(stray); err == nil {
		t.Error("rule between Layout spans accepted")
	}
	twice := append(append([]keyed(nil), rules...), keyed{Off: l[0].Off, Rule: Rule{Class: Never}})
	if err := validateRules(twice); err == nil {
		t.Error("rule keyed twice accepted")
	}
	if s := buildSpans(twice)[0]; s.Class != Unassigned || s.Field != l[0].Field {
		t.Errorf("span keyed twice built as %s %q, want UNASSIGNED %q", s.Class, s.Field, l[0].Field)
	}
}
