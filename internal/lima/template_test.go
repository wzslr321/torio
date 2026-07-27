/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   skills:
 *     - mark-ai-provenance
 */

package lima

import "testing"

func TestValidateOperatorUserAcceptsSafeNames(t *testing.T) {
	for _, name := range []string{"operator", "alice", "Bob_1", "a", "A_b-c"} {
		if err := validateOperatorUser(name); err != nil {
			t.Errorf("validateOperatorUser(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateOperatorUserRejectsInjection(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"$(id)",
		"`id`",
		"op er",
		"op;rm",
		"op|id",
		"op&id",
		"op/root",
		"op'x",
		"op\"x",
	}
	for _, name := range cases {
		if err := validateOperatorUser(name); err == nil {
			t.Errorf("validateOperatorUser(%q) = nil, want error", name)
		}
	}
}
