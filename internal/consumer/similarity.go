package consumer

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"paperless-go/internal/compress"
	"paperless-go/internal/database"
)

var wordRegex = regexp.MustCompile(`\p{L}{3,}`)

// FuzzyDuplicateMatch represents an identified near-duplicate document
type FuzzyDuplicateMatch struct {
	ID               int     `json:"id"`
	Title            string  `json:"title"`
	Similarity       float64 `json:"similarity"`
	Reason           string  `json:"reason"`
	OriginalFileName string  `json:"original_filename"`
}

// TokenizeWords splits a text into a set of normalized unique words
func TokenizeWords(text string) map[string]int {
	tokens := make(map[string]int)
	clean := strings.ToLower(text)
	matches := wordRegex.FindAllString(clean, -1)
	for _, m := range matches {
		if len(m) >= 3 {
			tokens[m]++
		}
	}
	return tokens
}

// TokenizeShingles extracts 2-word shingles for order-sensitive comparison
func TokenizeShingles(text string) map[string]bool {
	shingles := make(map[string]bool)
	clean := strings.ToLower(text)
	matches := wordRegex.FindAllString(clean, -1)
	for i := 0; i < len(matches)-1; i++ {
		shingle := matches[i] + " " + matches[i+1]
		shingles[shingle] = true
	}
	return shingles
}

// CalculateJaccardSimilarity computes the Jaccard similarity coefficient (0.0 to 1.0)
func CalculateJaccardSimilarity(text1, text2 string) float64 {
	if text1 == "" || text2 == "" {
		return 0.0
	}
	if text1 == text2 {
		return 1.0
	}

	set1 := TokenizeWords(text1)
	set2 := TokenizeWords(text2)

	if len(set1) == 0 || len(set2) == 0 {
		return 0.0
	}

	intersection := 0
	for word := range set1 {
		if _, exists := set2[word]; exists {
			intersection++
		}
	}

	union := len(set1) + len(set2) - intersection
	if union == 0 {
		return 0.0
	}

	wordJaccard := float64(intersection) / float64(union)

	// Shingle similarity for word sequence fidelity
	shingles1 := TokenizeShingles(text1)
	shingles2 := TokenizeShingles(text2)

	if len(shingles1) == 0 || len(shingles2) == 0 {
		return wordJaccard
	}

	shingleIntersection := 0
	for sh := range shingles1 {
		if shingles2[sh] {
			shingleIntersection++
		}
	}
	shingleUnion := len(shingles1) + len(shingles2) - shingleIntersection
	if shingleUnion == 0 {
		return wordJaccard
	}

	shingleJaccard := float64(shingleIntersection) / float64(shingleUnion)

	// Weighted combination: 60% words + 40% sequence shingles
	return math.Round((0.60*wordJaccard+0.40*shingleJaccard)*1000) / 1000
}

// FindFuzzyDuplicates scans the database using FTS5 candidate filtering and returns near-duplicates
func FindFuzzyDuplicates(db *database.DB, docID int, text string, threshold float64) ([]FuzzyDuplicateMatch, error) {
	if threshold <= 0.0 {
		threshold = 0.85 // Default 85% similarity threshold
	}

	tokens := TokenizeWords(text)
	if len(tokens) < 3 {
		return nil, nil
	}

	// Pick up to 5 distinctive words with len >= 4 for FTS candidate pruning
	var searchTerms []string
	for word := range tokens {
		if len(word) >= 4 && !isStopWord(word) {
			searchTerms = append(searchTerms, word)
			if len(searchTerms) >= 5 {
				break
			}
		}
	}

	var candidates []struct {
		id       int
		title    string
		content  string
		origName string
	}

	if len(searchTerms) > 0 {
		ftsQuery := strings.Join(searchTerms, " OR ")
		rows, err := db.Query(`
			SELECT d.id, d.title, d.content, d.original_filename
			FROM documents d
			WHERE d.id != ? AND d.deleted_at IS NULL
			  AND d.id IN (SELECT rowid FROM documents_fts WHERE documents_fts MATCH ?)
			LIMIT 30
		`, docID, ftsQuery)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var c struct {
					id       int
					title    string
					content  string
					origName string
				}
				if err := rows.Scan(&c.id, &c.title, &c.content, &c.origName); err == nil {
					candidates = append(candidates, c)
				}
			}
		}
	}

	// If FTS didn't return candidates, fetch recent documents as fallback
	if len(candidates) == 0 {
		rows, err := db.Query(`
			SELECT id, title, content, original_filename
			FROM documents
			WHERE id != ? AND deleted_at IS NULL
			ORDER BY id DESC LIMIT 20
		`, docID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var c struct {
					id       int
					title    string
					content  string
					origName string
				}
				if err := rows.Scan(&c.id, &c.title, &c.content, &c.origName); err == nil {
					candidates = append(candidates, c)
				}
			}
		}
	}

	var matches []FuzzyDuplicateMatch
	for _, cand := range candidates {
		candText := compress.DecompressText(cand.content)
		sim := CalculateJaccardSimilarity(text, candText)
		if sim >= threshold {
			matches = append(matches, FuzzyDuplicateMatch{
				ID:               cand.id,
				Title:            cand.title,
				Similarity:       sim,
				Reason:           "near_duplicate_text",
				OriginalFileName: cand.origName,
			})
		}
	}

	return matches, nil
}

func isStopWord(w string) bool {
	switch w {
	case "para", "como", "pelo", "pela", "este", "esta", "com", "por", "que", "uma", "isso",
		"this", "that", "with", "from", "have", "been", "were", "what", "which", "will":
		return true
	}
	for _, r := range w {
		if !unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
