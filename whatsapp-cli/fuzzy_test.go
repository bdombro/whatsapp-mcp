package main

import "testing"

func TestSequenceMatcherRatio(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"", "", 1.0},
		{"abc", "", 0.0},
		{"", "abc", 0.0},
		{"abc", "abc", 1.0},
		{"kevin", "kevin", 1.0},
		{"kevn", "kevin", 0.88},  // close match
		{"abc", "xyz", 0.0},      // no match
		{"abcde", "ace", 0.75},   // 3 matching out of 8 chars → 6/8 = 0.75
		{"family", "famly", 0.90}, // close match
	}
	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := sequenceMatcherRatio(tt.a, tt.b)
			if got < tt.want-0.05 || got > tt.want+0.05 {
				t.Errorf("sequenceMatcherRatio(%q, %q) = %.3f, want ~%.2f", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		query, text string
		want        bool
	}{
		// Substring matches
		{"alice", "Alice Smith", true},
		{"family", "Family Chat", true},
		{"xyz", "Alice Smith", false},

		// Short query — substring only
		{"al", "Alice Smith", true},
		{"zz", "Alice Smith", false},

		// Typo tolerance (≥3 chars)
		{"kevn", "Kevin", true},
		{"famly", "Family Chat", true},
		{"dombrwski", "Dombrowski", true},

		// Multi-word
		{"family chat", "Family Chat Room", true},
		{"chat family", "Family Chat Room", true},

		// Empty/nil
		{"test", "", false},
		{"", "something", true}, // empty query is substring of anything
	}
	for _, tt := range tests {
		t.Run(tt.query+"_in_"+tt.text, func(t *testing.T) {
			got := fuzzyMatch(tt.query, tt.text)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.query, tt.text, got, tt.want)
			}
		})
	}
}

func TestToLower(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ABC", "abc"},
		{"Hello World", "hello world"},
		{"already lower", "already lower"},
		{"MiXeD", "mixed"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := toLower(tt.in); got != tt.want {
			t.Errorf("toLower(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitWords(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"hello world", 2},
		{"  spaced  out  ", 2},
		{"single", 1},
		{"", 0},
		{"a b c d", 4},
	}
	for _, tt := range tests {
		if got := splitWords(tt.in); len(got) != tt.want {
			t.Errorf("splitWords(%q) gave %d words, want %d: %v", tt.in, len(got), tt.want, got)
		}
	}
}

func TestContainsSubstring(t *testing.T) {
	if !containsSubstring("hello world", "world") {
		t.Error("expected 'hello world' to contain 'world'")
	}
	if containsSubstring("hello", "world") {
		t.Error("expected 'hello' to NOT contain 'world'")
	}
	if !containsSubstring("abc", "") {
		t.Error("expected 'abc' to contain empty string")
	}
}
