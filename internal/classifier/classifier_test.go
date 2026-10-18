package classifier

import (
	"database/sql"
	"testing"

	"paperless-go/internal/database"
)

func TestClassifierTokenize(t *testing.T) {
	text := "Invoice #12345 from Electric Utility Company! Total due: $150.00 de luz e energia"
	tokens := Tokenize(text)

	// Stopwords like "from", "de", "e" should be omitted, numbers stripped
	for _, tok := range tokens {
		if tok == "from" || tok == "de" || tok == "e" || isAllDigits(tok) {
			t.Fatalf("Unexpected token in output: %s", tok)
		}
	}

	foundInvoice := false
	for _, tok := range tokens {
		if tok == "invoice" {
			foundInvoice = true
			break
		}
	}
	if !foundInvoice {
		t.Fatalf("Expected token 'invoice' in tokens: %v", tokens)
	}
}

func TestClassifierTrainingAndPrediction(t *testing.T) {
	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open memory db: %v", err)
	}
	defer rawDB.Close()

	db := &database.DB{DB: rawDB}
	_, err = db.Exec(database.SchemaSQL)
	if err != nil {
		t.Fatalf("Failed to init schema: %v", err)
	}

	// Insert tags, correspondents, doc types
	resT, _ := db.Exec("INSERT INTO tags (name, slug) VALUES ('Utility', 'utility')")
	tagID, _ := resT.LastInsertId()

	resC, _ := db.Exec("INSERT INTO correspondents (name, slug) VALUES ('Power Corp', 'power-corp')")
	corrID, _ := resC.LastInsertId()

	resDT, _ := db.Exec("INSERT INTO document_types (name, slug) VALUES ('Invoice', 'invoice')")
	typeID, _ := resDT.LastInsertId()

	// Insert training documents
	cID := int(corrID)
	tID := int(typeID)
	for i := 0; i < 3; i++ {
		resD, err := db.Exec(`
			INSERT INTO documents (title, content, correspondent_id, document_type_id, created, original_filename, checksum, mime_type)
			VALUES ('Electric Bill Monthly', 'Your monthly power consumption statement and electrical charges', ?, ?, CURRENT_TIMESTAMP, 'bill.pdf', ?, 'application/pdf')
		`, cID, tID, "chk"+string(rune(i+65)))
		if err != nil {
			t.Fatalf("Failed to insert doc: %v", err)
		}
		dID, _ := resD.LastInsertId()
		_, _ = db.Exec("INSERT INTO document_tags (document_id, tag_id) VALUES (?, ?)", dID, tagID)
	}

	clf := NewClassifier(db)
	err = clf.Train()
	if err != nil {
		t.Fatalf("Train failed: %v", err)
	}

	// Predict on new unseen document text
	newDocText := "Electrical energy charges power consumption statement invoice"
	pred := clf.Classify(newDocText)

	if len(pred.SuggestedTags) == 0 {
		t.Errorf("Expected suggested tags, got none")
	} else if pred.SuggestedTags[0] != int(tagID) {
		t.Errorf("Expected suggested tag %d, got %d", tagID, pred.SuggestedTags[0])
	}

	if pred.SuggestedCorrespondent == nil || *pred.SuggestedCorrespondent != cID {
		t.Errorf("Expected suggested correspondent %d, got %v", cID, pred.SuggestedCorrespondent)
	}

	if pred.SuggestedDocType == nil || *pred.SuggestedDocType != tID {
		t.Errorf("Expected suggested doc type %d, got %v", tID, pred.SuggestedDocType)
	}
}

