package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type ExtractedMetadata struct {
	InvoiceNumber *string  `json:"invoice_number"`
	TotalAmount   *float64 `json:"total_amount"`
	Currency      *string  `json:"currency"`
	IssueDate     *string  `json:"issue_date"`
	DueDate       *string  `json:"due_date"`
	TaxID         *string  `json:"tax_id"`
	IBAN          *string  `json:"iban"`
	Counterparty  *string  `json:"counterparty"`
	DocumentType  *string  `json:"document_type"`
	Summary       string   `json:"summary"`
}

type ExtractorService struct {
	provider Provider
}

func NewExtractorService(provider Provider) *ExtractorService {
	return &ExtractorService{provider: provider}
}

var jsonBlockRegex = regexp.MustCompile(`(?s)\x60\x60\x60(?:json)?\s*(\{.*?\})\s*\x60\x60\x60`)

func (e *ExtractorService) ExtractMetadata(ctx context.Context, title, content string) (*ExtractedMetadata, error) {
	if e.provider == nil {
		return nil, fmt.Errorf("AI provider not configured")
	}

	snippet := content
	if len(snippet) > 5000 {
		snippet = snippet[:5000]
	}

	systemPrompt := `You are an expert document analysis engine. Your job is to extract key financial and organizational metadata from documents into valid, strict JSON.
You MUST output ONLY a valid JSON object matching this schema, without conversational preamble or postscript:
{
  "invoice_number": "string or null",
  "total_amount": 123.45 or null,
  "currency": "EUR, USD, BRL, GBP, etc. or null",
  "issue_date": "YYYY-MM-DD or null",
  "due_date": "YYYY-MM-DD or null",
  "tax_id": "VAT / NIF / CNPJ / EIN / SSN or null",
  "iban": "International Bank Account Number without spaces or null",
  "counterparty": "Company, Vendor, or Person name or null",
  "document_type": "Invoice, Receipt, Contract, Bank Statement, Tax, Medical, or null",
  "summary": "Concise 1-sentence description of the document"
}`

	userPrompt := fmt.Sprintf("Title: %s\n\nDocument Text:\n%s", title, snippet)

	rawResponse, err := e.provider.Chat(ctx, systemPrompt, []ChatMessage{
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return nil, err
	}

	// Try extracting from ```json blocks if present
	cleanJSON := strings.TrimSpace(rawResponse)
	if matches := jsonBlockRegex.FindStringSubmatch(cleanJSON); len(matches) > 1 {
		cleanJSON = strings.TrimSpace(matches[1])
	} else {
		// Fallback: look for the first { and last }
		firstOpen := strings.Index(cleanJSON, "{")
		lastClose := strings.LastIndex(cleanJSON, "}")
		if firstOpen >= 0 && lastClose > firstOpen {
			cleanJSON = cleanJSON[firstOpen : lastClose+1]
		}
	}

	var meta ExtractedMetadata
	if err := json.Unmarshal([]byte(cleanJSON), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse AI JSON response: %w (raw response: %s)", err, rawResponse)
	}

	return &meta, nil
}

