package whatsapp

import (
	"database/sql"
	"strings"
	"time"
)

// sequenceMatcherRatio returns the similarity ratio between two strings,
// equivalent to Python's difflib.SequenceMatcher.ratio(). It computes
// 2.0 * M / T where M is the number of matching characters (LCS) and T
// is the total number of characters in both strings.
func sequenceMatcherRatio(a, b string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	ra := []rune(a)
	rb := []rune(b)
	m := len(ra)
	n := len(rb)

	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if ra[i-1] == rb[j-1] {
				curr[j] = prev[j-1] + 1
			} else if prev[j] > curr[j-1] {
				curr[j] = prev[j]
			} else {
				curr[j] = curr[j-1]
			}
		}
		prev, curr = curr, make([]int, n+1)
	}
	lcs := prev[n]
	return 2.0 * float64(lcs) / float64(m+n)
}

// fuzzyMatch checks whether query fuzzy-matches text (case-insensitive substring
// or word-level similarity with threshold 0.6).
func fuzzyMatch(query, text string) bool {
	return fuzzyMatchThreshold(query, text, 0.6)
}

// fuzzyMatchThreshold powers chat/contact discovery, using a caller-supplied threshold to balance typo tolerance against false positives.
func fuzzyMatchThreshold(query, text string, threshold float64) bool {
	if text == "" {
		return false
	}
	q := toLower(query)
	t := toLower(text)

	if containsSubstring(t, q) {
		return true
	}
	if len([]rune(q)) < 3 {
		return false
	}

	qWords := splitWords(q)
	tWords := splitWords(t)
	for _, qw := range qWords {
		found := false
		for _, tw := range tWords {
			if containsSubstring(tw, qw) || sequenceMatcherRatio(qw, tw) >= threshold {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// toLower centralizes lowercase normalization so fuzzy chat/contact matching stays consistent across helper paths.
func toLower(s string) string {
	return strings.ToLower(s)
}

// containsSubstring is the cheap pre-check fuzzy matching uses before paying for slower similarity work.
func containsSubstring(haystack, needle string) bool {
	return len(needle) <= len(haystack) && indexSubstring(haystack, needle) >= 0
}

// indexSubstring is the byte-level substring primitive behind the custom fuzzy matcher, returning -1 when `sub` is absent.
func indexSubstring(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

// splitWords splits s on ASCII whitespace so fuzzy matching can compare word-by-word.
func splitWords(s string) []string {
	var words []string
	start := -1
	for i, c := range s {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if start >= 0 {
				words = append(words, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		words = append(words, s[start:])
	}
	return words
}

// nullStr unwraps nullable SQL strings so MCP formatting and search indexing can avoid repeating `.Valid` checks everywhere.
func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// jidPhone returns the phone/user part before the WhatsApp JID suffix.
func jidPhone(jid string) string {
	if idx := strings.Index(jid, "@"); idx > 0 {
		return jid[:idx]
	}
	return jid
}

// parseTime parses the timestamp formats stored across WhatsApp sync paths into time.Time.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05-07:00", s)
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04:05", s)
			if err != nil {
				t, _ = time.Parse("2006-01-02T15:04:05Z", s)
			}
		}
	}
	return t
}
