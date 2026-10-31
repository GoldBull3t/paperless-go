package consumer

import (
	"testing"
)

func TestCalculateJaccardSimilarity(t *testing.T) {
	doc1 := `FATURA / RECIBO N. 2026/001
Cliente: Bernardo Barreiras
Fornecedor: EDP Comercial S.A.
Valor Total a Pagar: 85.50 EUR
Data de Vencimento: 2026-11-15
Consumo de Eletricidade referente ao periodo de Outubro.`

	// Re-scanned or converted document with slightly different formatting or date
	doc2 := `FATURA / RECIBO N. 2026/002
Cliente: Bernardo Barreiras
Fornecedor: EDP Comercial S.A.
Valor Total a Pagar: 85.50 EUR
Data de Vencimento: 2026-11-16
Consumo de Eletricidade referente ao periodo de Outubro.`

	// Completely unrelated document
	doc3 := `CONTRATO DE ARRENDAMENTO HABITACIONAL
Primeiro Outorgante: Senhorio
Segundo Outorgante: Inquilino
Imovel situado na Rua Principal n 10.`

	simIdentical := CalculateJaccardSimilarity(doc1, doc1)
	if simIdentical != 1.0 {
		t.Errorf("Expected 1.0 for identical text, got %f", simIdentical)
	}

	simNear := CalculateJaccardSimilarity(doc1, doc2)
	if simNear < 0.85 {
		t.Errorf("Expected >= 0.85 for near-duplicate documents, got %f", simNear)
	}

	simDiff := CalculateJaccardSimilarity(doc1, doc3)
	if simDiff > 0.20 {
		t.Errorf("Expected <= 0.20 for completely different documents, got %f", simDiff)
	}
}
