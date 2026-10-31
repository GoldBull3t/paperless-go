package search_test

import (
	"testing"

	"paperless-go/internal/search"
)

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "    ",
			expected: "",
		},
		{
			name:     "single word prefix",
			input:    "factura",
			expected: "factura*",
		},
		{
			name:     "multiple words with accents",
			input:    "eletricidade água",
			expected: "eletricidade* água*",
		},
		{
			name:     "quoted exact phrase",
			input:    `"EDP Comercial" 2026`,
			expected: `"EDP Comercial" 2026*`,
		},
		{
			name:     "unclosed quote auto-balanced",
			input:    `"fatura luz`,
			expected: `"fatura luz"`,
		},
		{
			name:     "special punctuation stripped",
			input:    "hello (world) [foo] {bar} * ? ^",
			expected: "hello* world* foo* bar*",
		},
		{
			name:     "reserved operators quoted as literals",
			input:    "AND OR NOT",
			expected: `"AND" "OR" "NOT"`,
		},
		{
			name:     "single letter words without wildcard",
			input:    "a b c test",
			expected: "a b c test*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := search.SanitizeFTS5Query(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFTS5Query(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBM25RankExpression(t *testing.T) {
	expr := search.BM25RankExpression(5.0, 1.0)
	expected := "bm25(documents_fts, 5.0, 1.0)"
	if expr != expected {
		t.Errorf("BM25RankExpression = %q; want %q", expr, expected)
	}
}
