package policy

import "testing"

func TestMatchesTerms(t *testing.T) {
	cases := []struct {
		text  string
		terms []string
		want  bool
	}{
		{"week 10/notes [a].pdf", []string{"notes", "week 10"}, true},
		{"week 10/notes [a].pdf", []string{"[a].", ".pdf"}, true},
		{"week 10/notes [a].pdf", []string{"notes", "absent"}, false},
		{"week 10/notes [a].pdf", []string{".*"}, false},
		{"中文🎵/notes", []string{"🎵", "中文"}, true},
		{"prepared lowercase", []string{"Lowercase"}, false},
		{"", nil, true}, {"anything", []string{""}, true},
		{"abc", []string{"abcd"}, false},
	}
	for _, c := range cases {
		if got := MatchesTerms(c.text, c.terms); got != c.want {
			t.Errorf("MatchesTerms(%q,%q)=%v want %v", c.text, c.terms, got, c.want)
		}
	}
}
