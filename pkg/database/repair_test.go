package database

import "testing"

func TestNormalizeDeleteRule(t *testing.T) {
	cases := map[string]string{
		"CASCADE":     "CASCADE",
		"cascade":     "CASCADE",
		"SET NULL":    "SET NULL",
		"set null":    "SET NULL",
		"SET DEFAULT": "SET DEFAULT",
		"RESTRICT":    "RESTRICT",
		"NO ACTION":   "NO ACTION",
		"":            "NO ACTION",
		"  ":          "NO ACTION",
	}
	for in, want := range cases {
		if got := normalizeDeleteRule(in); got != want {
			t.Errorf("normalizeDeleteRule(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPqQuoteIdent(t *testing.T) {
	if got := pqQuoteIdent(`sessions`); got != `"sessions"` {
		t.Errorf("got %s", got)
	}
	if got := pqQuoteIdent(`weird"name`); got != `"weird""name"` {
		t.Errorf("got %s", got)
	}
}
