package ai

import (
	"context"
	"fmt"
	"strings"
)

type DocumentContext struct {
	ID        int
	Title     string
	Created   string
	Content   string
	Score     float32
}

type RAGService struct {
	provider Provider
}

func NewRAGService(provider Provider) *RAGService {
	return &RAGService{provider: provider}
}

func (s *RAGService) ChatWithDocuments(ctx context.Context, userQuery string, docs []DocumentContext, history []ChatMessage) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("AI provider not available")
	}

	var sb strings.Builder
	sb.WriteString("You are the intelligent AI assistant of Paperless-go. You help users understand, analyze, summarize, and extract information from their stored documents.\n")
	sb.WriteString("Answer the user's questions clearly, accurately, and concisely based strictly on the provided document contexts below.\n")
	sb.WriteString("If the answer cannot be determined from the documents, politely state that the information was not found.\n")
	sb.WriteString("Always mention the document title or ID when citing facts.\n\n")
	sb.WriteString("=== AVAILABLE DOCUMENTS CONTEXT ===\n")

	for i, d := range docs {
		// Truncate individual document content to avoid token overflow (~4000 characters per doc)
		content := d.Content
		if len(content) > 4000 {
			content = content[:4000] + "\n...[truncated]"
		}
		sb.WriteString(fmt.Sprintf("\n--- Document #%d (ID: %d): %s (Created: %s) ---\n%s\n", i+1, d.ID, d.Title, d.Created, content))
	}
	sb.WriteString("=== END CONTEXT ===\n")

	messages := append(history, ChatMessage{
		Role:    "user",
		Content: userQuery,
	})

	return s.provider.Chat(ctx, sb.String(), messages)
}

func (s *RAGService) SummarizeDocument(ctx context.Context, title string, content string) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("AI provider not available")
	}
	if len(content) > 6000 {
		content = content[:6000] + "\n...[truncated]"
	}

	systemPrompt := "You are an executive document summarizer. Summarize the following document in 3 concise bullet points with key highlights (dates, amounts, counterparties, obligations), followed by a 1-sentence conclusion."
	userPrompt := fmt.Sprintf("Document Title: %s\n\nContent:\n%s", title, content)

	return s.provider.Chat(ctx, systemPrompt, []ChatMessage{
		{Role: "user", Content: userPrompt},
	})
}

func (s *RAGService) CompareDocuments(ctx context.Context, docA, docB DocumentContext) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("AI provider not available")
	}

	systemPrompt := "You are an analytical document comparison assistant. Compare the two provided documents side by side. Highlight differences in dates, amounts, terms, counterparties, and any notable discrepancies or trends."

	contentA := docA.Content
	if len(contentA) > 3500 {
		contentA = contentA[:3500] + "..."
	}
	contentB := docB.Content
	if len(contentB) > 3500 {
		contentB = contentB[:3500] + "..."
	}

	userPrompt := fmt.Sprintf("=== DOCUMENT 1 (ID: %d, %s) ===\n%s\n\n=== DOCUMENT 2 (ID: %d, %s) ===\n%s\n\nPlease compare them comprehensively.",
		docA.ID, docA.Title, contentA,
		docB.ID, docB.Title, contentB,
	)

	return s.provider.Chat(ctx, systemPrompt, []ChatMessage{
		{Role: "user", Content: userPrompt},
	})
}

