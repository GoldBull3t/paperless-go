package workflow

import (
	"regexp"
	"strings"
)

// Matching algorithms matching Paperless-ngx constants
const (
	MatchAuto     = 0
	MatchAnyWord  = 1
	MatchAllWords = 2
	MatchExact    = 3
	MatchRegex    = 4
	MatchFuzzy    = 5
)

// MatchesRule checks if the text (content + title) matches the rule expression according to the matching algorithm.
func MatchesRule(content, title, match string, algo int, insensitive bool) bool {
	cleanMatch := strings.TrimSpace(match)
	if cleanMatch == "" {
		return false
	}
	text := content + " " + title
	if insensitive {
		text = strings.ToLower(text)
		cleanMatch = strings.ToLower(cleanMatch)
	}

	switch algo {
	case MatchAnyWord:
		words := strings.Fields(cleanMatch)
		for _, w := range words {
			if strings.Contains(text, w) {
				return true
			}
		}
		return false

	case MatchAllWords:
		words := strings.Fields(cleanMatch)
		if len(words) == 0 {
			return false
		}
		for _, w := range words {
			if !strings.Contains(text, w) {
				return false
			}
		}
		return true

	case MatchExact:
		return strings.Contains(text, cleanMatch)

	case MatchRegex:
		expr := cleanMatch
		if insensitive && !strings.HasPrefix(expr, "(?i)") {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err == nil {
			return re.MatchString(text)
		}
		return false

	case MatchFuzzy:
		// Fast path: exact substring match
		if strings.Contains(text, cleanMatch) {
			return true
		}
		// Word-level fuzzy matching with Levenshtein tolerance
		matchWords := strings.Fields(cleanMatch)
		textWords := strings.Fields(text)
		if len(matchWords) == 0 || len(textWords) == 0 {
			return false
		}
		allMatched := true
		for _, mw := range matchWords {
			mwLen := len(mw)
			maxDist := 1
			if mwLen > 6 {
				maxDist = 2
			} else if mwLen < 4 {
				maxDist = 0
			}
			found := false
			for _, tw := range textWords {
				if maxDist == 0 {
					if tw == mw {
						found = true
						break
					}
				} else if abs(len(tw)-mwLen) <= maxDist {
					if levenshteinDistance(tw, mw) <= maxDist {
						found = true
						break
					}
				}
			}
			if !found {
				allMatched = false
				break
			}
		}
		return allMatched

	default:
		// Default to any word match if algo is not specified
		words := strings.Fields(cleanMatch)
		for _, w := range words {
			if strings.Contains(text, w) {
				return true
			}
		}
		return false
	}
}

func levenshteinDistance(s, t string) int {
	d := make([][]int, len(s)+1)
	for i := range d {
		d[i] = make([]int, len(t)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for j := 1; j <= len(t); j++ {
		for i := 1; i <= len(s); i++ {
			if s[i-1] == t[j-1] {
				d[i][j] = d[i-1][j-1]
			} else {
				minVal := d[i-1][j] + 1
				if d[i][j-1]+1 < minVal {
					minVal = d[i][j-1] + 1
				}
				if d[i-1][j-1]+1 < minVal {
					minVal = d[i-1][j-1] + 1
				}
				d[i][j] = minVal
			}
		}
	}
	return d[len(s)][len(t)]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
