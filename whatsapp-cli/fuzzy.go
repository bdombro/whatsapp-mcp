package main

// sequenceMatcherRatio returns the similarity ratio between two strings,
// equivalent to Python's difflib.SequenceMatcher.ratio(). It computes
// 2.0 * M / T where M is the number of matching characters (longest
// common subsequence) and T is the total number of characters in both strings.
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

	// LCS via two-row DP
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

// fuzzyMatch checks whether query fuzzy-matches text using case-insensitive
// substring matching followed by word-level similarity (threshold 0.6).
// For queries shorter than 3 characters only exact substring matching is used.
func fuzzyMatch(query, text string) bool {
	return fuzzyMatchThreshold(query, text, 0.6)
}

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

// toLower is a simple ASCII-aware lowercaser (sufficient for names/JIDs).
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func containsSubstring(haystack, needle string) bool {
	return len(needle) <= len(haystack) && indexSubstring(haystack, needle) >= 0
}

func indexSubstring(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

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
