package book

import "testing"

func TestNormTitle(t *testing.T) {
	cases := map[string]string{
		"The Shadow Of What Was Lost (The Licanius Trilogy Book 1)": "shadow of what was lost",
		"The Practice Effect: A Novel":                              "practice effect",
		"A Parade of Horribles":                                     "parade of horribles",
		"Games Wizards Play":                                        "games wizards play",
		"Café Society":                                              "cafe society",
		"Wizard's First Rule":                                       "wizards first rule",
	}
	for in, want := range cases {
		if got := NormTitle(in); got != want {
			t.Errorf("NormTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormName(t *testing.T) {
	if a, b := NormName("James S. A. Corey"), NormName("James S.A. Corey"); a != b || a != "james corey" {
		t.Errorf("%q vs %q", a, b)
	}
}

func TestMatch(t *testing.T) {
	local := Local{Title: "The Strength of the Few", Authors: []string{"James Islington"}}
	cands := []Candidate{
		{Title: "The Will of the Many", Authors: []string{"James Islington"}},
		{Title: "The Strength of the Few", Authors: []string{"James Islington"}},
	}
	if got := Match(local, cands); got != 1 {
		t.Errorf("match = %d", got)
	}
	// Same title, other author: no match.
	if got := Match(local, []Candidate{{Title: "The Strength of the Few", Authors: []string{"Someone Else"}}}); got != -1 {
		t.Errorf("wrong author matched: %d", got)
	}
	// Two equal candidates: ambiguous, never guess.
	if got := Match(local, []Candidate{cands[1], cands[1]}); got != -1 {
		t.Errorf("ambiguous matched: %d", got)
	}
}

func TestPercentToPage(t *testing.T) {
	cases := []struct {
		pct   float64
		pages int
		want  int
	}{
		{10.723627, 400, 42}, {0, 400, 0}, {0.01, 400, 1}, {100, 400, 400}, {150, 400, 400}, {50, 0, 0},
	}
	for _, c := range cases {
		if got := PercentToPage(c.pct, c.pages); got != c.want {
			t.Errorf("PercentToPage(%v, %d) = %d, want %d", c.pct, c.pages, got, c.want)
		}
	}
}
