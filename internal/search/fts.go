package search

import (
	"fmt"
	"strings"
	"unicode"
)

var ftsReserved = map[string]bool{
	"AND":  true,
	"OR":   true,
	"NOT":  true,
	"NEAR": true,
}

// SanitizeFTS5Query prepares user input into a safe and efficient SQLite FTS5 query string.
// Features:
// 1. Quoted phrase preservation ("exact phrase" matches)
// 2. Safe alphanumeric and accented unicode parsing
// 3. Prefix matching (word*) for terms with 2+ characters
// 4. Neutralization of syntax breaking characters (^, *, :, (, ), OR, AND, NOT)
// 5. Unbalanced quotes auto-closure
func SanitizeFTS5Query(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var phrases []string
	var unquotedRunes []rune

	inQuote := false
	var currentQuote strings.Builder

	runes := []rune(trimmed)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '"' {
			if inQuote {
				// closing quote
				inQuote = false
				phrase := cleanPhrase(currentQuote.String())
				if phrase != "" {
					phrases = append(phrases, `"`+phrase+`"`)
				}
				currentQuote.Reset()
			} else {
				// opening quote
				inQuote = true
			}
		} else if inQuote {
			currentQuote.WriteRune(r)
		} else {
			unquotedRunes = append(unquotedRunes, r)
		}
	}

	// Auto-close unbalanced quote
	if inQuote {
		phrase := cleanPhrase(currentQuote.String())
		if phrase != "" {
			phrases = append(phrases, `"`+phrase+`"`)
		}
	}

	// Process unquoted terms
	var cleanTerms []string
	var curTerm strings.Builder
	for _, r := range unquotedRunes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			curTerm.WriteRune(r)
		} else {
			if curTerm.Len() > 0 {
				token := curTerm.String()
				curTerm.Reset()
				if formatted := formatToken(token); formatted != "" {
					cleanTerms = append(cleanTerms, formatted)
				}
			}
		}
	}
	if curTerm.Len() > 0 {
		token := curTerm.String()
		if formatted := formatToken(token); formatted != "" {
			cleanTerms = append(cleanTerms, formatted)
		}
	}

	var allParts []string
	allParts = append(allParts, phrases...)
	allParts = append(allParts, cleanTerms...)

	return strings.Join(allParts, " ")
}

func cleanPhrase(p string) string {
	var b strings.Builder
	for _, r := range p {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
}

func formatToken(token string) string {
	upper := strings.ToUpper(token)
	if ftsReserved[upper] {
		// Neutralize operator by quoting it as a literal string
		return `"` + token + `"`
	}

	runes := []rune(token)
	if len(runes) >= 2 {
		return token + "*"
	}
	if len(runes) == 1 {
		return token
	}
	return ""
}

// BM25RankExpression returns the SQLite FTS5 bm25 function with custom column weights
// weightTitle prioritizes title matches over content matches
func BM25RankExpression(weightTitle, weightContent float64) string {
	return fmt.Sprintf("bm25(documents_fts, %.1f, %.1f)", weightTitle, weightContent)
}
