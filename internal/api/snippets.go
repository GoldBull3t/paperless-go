package api

import (
	"regexp"
	"strings"

	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

// GenerateFallbackSnippet produces a text snippet with <mark>...</mark> surrounding matched terms
func GenerateFallbackSnippet(content, query string, maxTokens int) string {
	if content == "" || query == "" {
		return ""
	}
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return ""
	}

	lowerContent := strings.ToLower(content)
	firstPos := -1

	for _, t := range terms {
		tClean := strings.ToLower(strings.Trim(t, `"'*?~^`))
		if tClean == "" || strings.EqualFold(tClean, "and") || strings.EqualFold(tClean, "or") || strings.EqualFold(tClean, "not") {
			continue
		}
		if idx := strings.Index(lowerContent, tClean); idx >= 0 {
			if firstPos == -1 || idx < firstPos {
				firstPos = idx
			}
		}
	}

	if firstPos == -1 {
		return ""
	}

	charWindow := maxTokens * 6
	start := firstPos - (charWindow / 2)
	prefix := "..."
	if start <= 0 {
		start = 0
		prefix = ""
	} else {
		for start > 0 && content[start] != ' ' && content[start] != '\n' {
			start--
		}
		start++
	}

	end := start + charWindow
	suffix := "..."
	if end >= len(content) {
		end = len(content)
		suffix = ""
	} else {
		for end < len(content) && content[end] != ' ' && content[end] != '\n' {
			end++
		}
	}

	if start > end {
		start = end
	}
	snippetText := content[start:end]

	for _, t := range terms {
		tClean := strings.Trim(t, `"'*?~^`)
		if tClean == "" || strings.EqualFold(tClean, "and") || strings.EqualFold(tClean, "or") || strings.EqualFold(tClean, "not") {
			continue
		}
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(tClean))
		if err == nil {
			snippetText = re.ReplaceAllStringFunc(snippetText, func(m string) string {
				return "<mark>" + m + "</mark>"
			})
		}
	}

	return prefix + strings.TrimSpace(snippetText) + suffix
}

// BuildSearchHits populates doc.SearchHit for a slice of documents based on FTS5 snippets and fallback highlights
func BuildSearchHits(db *database.DB, docs []*models.Document, query string) {
	if len(docs) == 0 || strings.TrimSpace(query) == "" {
		return
	}

	safeFTS := sanitizeFTSQuery(query)
	ftsSnippets := make(map[int]string)

	if safeFTS != "" {
		rows, err := db.Query("SELECT rowid, snippet(documents_fts, -1, '<mark>', '</mark>', '...', 32) FROM documents_fts WHERE documents_fts MATCH ?", safeFTS)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int
				var snip string
				if err := rows.Scan(&id, &snip); err == nil && snip != "" {
					ftsSnippets[id] = snip
				}
			}
		}
	}

	// Notes snippets
	noteSnippets := make(map[int][]string)
	noteRows, err := db.Query("SELECT document_id, note FROM document_notes WHERE note LIKE ?", "%"+query+"%")
	if err == nil {
		defer noteRows.Close()
		for noteRows.Next() {
			var docID int
			var noteText string
			if err := noteRows.Scan(&docID, &noteText); err == nil {
				snip := GenerateFallbackSnippet(noteText, query, 32)
				if snip != "" {
					noteSnippets[docID] = append(noteSnippets[docID], snip)
				}
			}
		}
	}

	for i, doc := range docs {
		var highlights []string
		if snip, ok := ftsSnippets[doc.ID]; ok && snip != "" {
			highlights = append(highlights, snip)
		} else {
			if fb := GenerateFallbackSnippet(doc.Content, query, 32); fb != "" {
				highlights = append(highlights, fb)
			} else if strings.Contains(strings.ToLower(doc.Title), strings.ToLower(query)) {
				highlights = append(highlights, GenerateFallbackSnippet(doc.Title, query, 32))
			}
		}
		if highlights == nil {
			highlights = []string{}
		}

		var noteHls []string
		if nh, ok := noteSnippets[doc.ID]; ok {
			noteHls = nh
		} else {
			noteHls = []string{}
		}

		doc.SearchHit = &models.SearchHit{
			Score:          1.0,
			Highlights:     highlights,
			NoteHighlights: noteHls,
			Rank:           i + 1,
		}
	}
}
