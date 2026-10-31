package workflow

import (
	"testing"
)

func TestMatchesRule_AnyWord(t *testing.T) {
	content := "The electric invoice for November was processed"
	title := "Electric Bill"

	if !MatchesRule(content, title, "water invoice gas", MatchAnyWord, true) {
		t.Errorf("Expected match for 'invoice' in content")
	}

	if MatchesRule(content, title, "solar wind hydro", MatchAnyWord, true) {
		t.Errorf("Did not expect match for unrelated terms")
	}
}

func TestMatchesRule_AllWords(t *testing.T) {
	content := "The quarterly tax return was filed by John"
	title := "Tax 2026"

	if !MatchesRule(content, title, "quarterly tax return", MatchAllWords, true) {
		t.Errorf("Expected all words to match")
	}

	if MatchesRule(content, title, "quarterly tax audit", MatchAllWords, true) {
		t.Errorf("Did not expect match because 'audit' is missing")
	}
}

func TestMatchesRule_Exact(t *testing.T) {
	content := "Invoice number: INV-2026-9901 issued on Friday"
	title := "Vendor Doc"

	if !MatchesRule(content, title, "INV-2026-9901", MatchExact, true) {
		t.Errorf("Expected exact match")
	}

	if MatchesRule(content, title, "INV-2026-9902", MatchExact, true) {
		t.Errorf("Did not expect exact match for different number")
	}
}

func TestMatchesRule_Regex(t *testing.T) {
	content := "Reference: REF-[A-Z]{3}-[0-9]{4} -> REF-XYZ-8812 verified"
	title := "Contract"

	if !MatchesRule(content, title, `REF-[A-Z]{3}-\d{4}`, MatchRegex, true) {
		t.Errorf("Expected regex match")
	}

	if MatchesRule(content, title, `ORDER-\d{5}`, MatchRegex, true) {
		t.Errorf("Did not expect match for ORDER regex")
	}
}

func TestMatchesRule_Fuzzy(t *testing.T) {
	content := "Document from Telecom Portugal regarding broadband"
	title := "Telecom Bill"

	// Match with small typo "Telecon"
	if !MatchesRule(content, title, "Telecon", MatchFuzzy, true) {
		t.Errorf("Expected fuzzy match for 'Telecon' against 'Telecom'")
	}

	// Match with small typo "broadbamd"
	if !MatchesRule(content, title, "broadbamd", MatchFuzzy, true) {
		t.Errorf("Expected fuzzy match for 'broadbamd' against 'broadband'")
	}

	// Unrelated term should not match
	if MatchesRule(content, title, "Aerospace", MatchFuzzy, true) {
		t.Errorf("Did not expect fuzzy match for completely different word")
	}
}
