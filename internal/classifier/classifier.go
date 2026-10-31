package classifier

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"
	"unicode"

	"paperless-go/internal/compress"
	"paperless-go/internal/database"
)

var defaultStopWords = makeStopWordsSet([]string{
	// English
	"a", "about", "above", "after", "again", "against", "all",
	"am", "an", "and", "any", "are", "as", "at", "be",
	"because", "been", "before", "being", "below", "between", "both",
	"but", "by", "can", "did", "do", "does", "doing", "down",
	"during", "each", "few", "for", "from", "further", "had",
	"has", "have", "having", "he", "her", "here", "hers",
	"herself", "him", "himself", "his", "how", "i", "if",
	"in", "into", "is", "it", "its", "itself", "just", "me",
	"more", "most", "my", "myself", "no", "nor", "not", "now",
	"of", "off", "on", "once", "only", "or", "other", "our",
	"ours", "ourselves", "out", "over", "own", "same", "she",
	"should", "so", "some", "such", "than", "that", "the",
	"their", "theirs", "them", "themselves", "then", "there",
	"these", "they", "this", "those", "through", "to", "too",
	"under", "until", "up", "very", "was", "we", "were",
	"what", "when", "where", "which", "while", "who", "whom",
	"why", "with", "would", "you", "your", "yours", "yourself",
	// Portuguese
	"de", "a", "o", "que", "e", "do", "da", "em", "um",
	"para", "com", "nao", "uma", "os", "no", "se", "na",
	"por", "mais", "as", "dos", "como", "mas", "foi", "ao",
	"ele", "das", "tem", "seu", "sua", "ou", "ser", "quando",
	"muito", "nos", "ja", "está", "eu", "tambem", "so", "pelo",
	"pela", "ate", "isso", "ela", "entre", "era", "depois",
	"sem", "mesmo", "aos", "ter", "seus", "quem", "nas", "me",
	"esse", "eles", "estao", "voce", "tinha", "foram", "essa",
	"num", "nem", "suas", "meu", "as", "minha", "numa", "pelos",
	"elas", "havia", "seja", "qual", "sera", "nos", "tenho",
	// German
	"der", "die", "das", "und", "ist", "in", "den", "von",
	"zu", "mit", "sich", "des", "auf", "fur", "fuer", "ist",
	"im", "dem", "nicht", "ein", "eine", "als", "auch", "es",
	"an", "werden", "aus", "er", "hat", "dass", "sie", "nach",
	"wird", "bei", "einer", "um", "am", "sind", "noch", "wie",
})

func makeStopWordsSet(words []string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

type Classifier struct {
	mu           sync.RWMutex
	db           *database.DB
	idf          map[string]float64
	tagVecs      map[int]map[string]float64
	tagNorms     map[int]float64
	corrVecs     map[int]map[string]float64
	corrNorms    map[int]float64
	docTypeVecs  map[int]map[string]float64
	docTypeNorms map[int]float64
	pathVecs     map[int]map[string]float64
	pathNorms    map[int]float64
}

func NewClassifier(db *database.DB) *Classifier {
	c := &Classifier{
		db:           db,
		idf:          make(map[string]float64),
		tagVecs:      make(map[int]map[string]float64),
		tagNorms:     make(map[int]float64),
		corrVecs:     make(map[int]map[string]float64),
		corrNorms:    make(map[int]float64),
		docTypeVecs:  make(map[int]map[string]float64),
		docTypeNorms: make(map[int]float64),
		pathVecs:     make(map[int]map[string]float64),
		pathNorms:    make(map[int]float64),
	}
	_ = c.Train()
	return c
}

// Tokenize splits text into lowercase words, stripping non-letter chars and filtering stopwords
func Tokenize(text string) []string {
	var words []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else {
			if current.Len() >= 2 {
				w := current.String()
				if !defaultStopWords[w] && !isAllDigits(w) {
					words = append(words, w)
				}
			}
			current.Reset()
		}
	}
	if current.Len() >= 2 {
		w := current.String()
		if !defaultStopWords[w] && !isAllDigits(w) {
			words = append(words, w)
		}
	}

	return words
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func computeTF(tokens []string) map[string]float64 {
	tf := make(map[string]float64)
	if len(tokens) == 0 {
		return tf
	}
	total := float64(len(tokens))
	for _, tok := range tokens {
		tf[tok]++
	}
	for tok, count := range tf {
		// Sublinear term frequency scaling
		tf[tok] = (1.0 + math.Log(count)) / total
	}
	return tf
}

// Train reads all documents from the database and constructs TF-IDF vectors
func (c *Classifier) Train() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	rows, err := c.db.Query(`
		SELECT id, title, content, correspondent_id, document_type_id, storage_path_id
		FROM documents WHERE deleted_at IS NULL
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type docRecord struct {
		id      int
		tokens  []string
		corrID  *int
		typeID  *int
		pathID  *int
		tags    []int
	}

	var docs []docRecord
	docFreq := make(map[string]int)

	for rows.Next() {
		var id int
		var title, content string
		var corrID, typeID, pathID sql.NullInt64

		if err := rows.Scan(&id, &title, &content, &corrID, &typeID, &pathID); err == nil {
			content = compress.DecompressText(content)
			tokens := Tokenize(title + " " + content)
			if len(tokens) == 0 {
				continue
			}

			seenInDoc := make(map[string]bool)
			for _, tok := range tokens {
				seenInDoc[tok] = true
			}
			for tok := range seenInDoc {
				docFreq[tok]++
			}

			rec := docRecord{
				id:     id,
				tokens: tokens,
			}
			if corrID.Valid {
				c := int(corrID.Int64)
				rec.corrID = &c
			}
			if typeID.Valid {
				t := int(typeID.Int64)
				rec.typeID = &t
			}
			if pathID.Valid {
				p := int(pathID.Int64)
				rec.pathID = &p
			}
			docs = append(docs, rec)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("classifier docs query cursor error: %w", err)
	}
	rows.Close()

	// Load document tags
	for i := range docs {
		tagRows, err := c.db.Query("SELECT tag_id FROM document_tags WHERE document_id = ?", docs[i].id)
		if err == nil {
			for tagRows.Next() {
				var tid int
				if err := tagRows.Scan(&tid); err == nil {
					docs[i].tags = append(docs[i].tags, tid)
				}
			}
			if err := tagRows.Err(); err != nil {
				tagRows.Close()
				return fmt.Errorf("classifier doc tags cursor error for doc %d: %w", docs[i].id, err)
			}
			tagRows.Close()
		}
	}

	totalDocs := float64(len(docs))
	if totalDocs == 0 {
		return nil
	}

	// Compute IDF: idf(t) = log(1 + totalDocs / (1 + docFreq(t))) + 1.0
	c.idf = make(map[string]float64)
	for tok, df := range docFreq {
		c.idf[tok] = math.Log(1.0+(totalDocs/float64(1+df))) + 1.0
	}

	// Build Category TF-IDF aggregations
	c.tagVecs = make(map[int]map[string]float64)
	c.tagNorms = make(map[int]float64)
	c.corrVecs = make(map[int]map[string]float64)
	c.corrNorms = make(map[int]float64)
	c.docTypeVecs = make(map[int]map[string]float64)
	c.docTypeNorms = make(map[int]float64)
	c.pathVecs = make(map[int]map[string]float64)
	c.pathNorms = make(map[int]float64)

	for _, doc := range docs {
		docVec := c.vectorizeTokens(doc.tokens)

		// Aggregate for tags
		for _, tid := range doc.tags {
			if c.tagVecs[tid] == nil {
				c.tagVecs[tid] = make(map[string]float64)
			}
			for tok, val := range docVec {
				c.tagVecs[tid][tok] += val
			}
		}

		// Aggregate for correspondent
		if doc.corrID != nil {
			cid := *doc.corrID
			if c.corrVecs[cid] == nil {
				c.corrVecs[cid] = make(map[string]float64)
			}
			for tok, val := range docVec {
				c.corrVecs[cid][tok] += val
			}
		}

		// Aggregate for docType
		if doc.typeID != nil {
			dtid := *doc.typeID
			if c.docTypeVecs[dtid] == nil {
				c.docTypeVecs[dtid] = make(map[string]float64)
			}
			for tok, val := range docVec {
				c.docTypeVecs[dtid][tok] += val
			}
		}

		// Aggregate for storage path
		if doc.pathID != nil {
			pid := *doc.pathID
			if c.pathVecs[pid] == nil {
				c.pathVecs[pid] = make(map[string]float64)
			}
			for tok, val := range docVec {
				c.pathVecs[pid][tok] += val
			}
		}
	}

	// Calculate norms for all aggregated vectors
	for id, vec := range c.tagVecs {
		c.tagNorms[id] = computeNorm(vec)
	}
	for id, vec := range c.corrVecs {
		c.corrNorms[id] = computeNorm(vec)
	}
	for id, vec := range c.docTypeVecs {
		c.docTypeNorms[id] = computeNorm(vec)
	}
	for id, vec := range c.pathVecs {
		c.pathNorms[id] = computeNorm(vec)
	}

	return nil
}

func (c *Classifier) vectorizeTokens(tokens []string) map[string]float64 {
	tf := computeTF(tokens)
	vec := make(map[string]float64)
	for tok, tfVal := range tf {
		idfVal, ok := c.idf[tok]
		if !ok {
			idfVal = 1.0
		}
		vec[tok] = tfVal * idfVal
	}
	return vec
}

func computeNorm(vec map[string]float64) float64 {
	sum := 0.0
	for _, v := range vec {
		sum += v * v
	}
	return math.Sqrt(sum)
}

func cosineSimilarity(v1 map[string]float64, norm1 float64, v2 map[string]float64, norm2 float64) float64 {
	if norm1 == 0 || norm2 == 0 {
		return 0.0
	}
	dot := 0.0
	// Iterate through the smaller vector for efficiency
	small, large := v1, v2
	if len(v2) < len(v1) {
		small, large = v2, v1
	}
	for tok, val1 := range small {
		if val2, ok := large[tok]; ok {
			dot += val1 * val2
		}
	}
	return dot / (norm1 * norm2)
}

type ClassificationResult struct {
	SuggestedTags        []int
	SuggestedCorrespondent *int
	SuggestedDocType     *int
	SuggestedStoragePath *int
	TagScores            map[int]float64
}

// Classify predicts tags, correspondent, and document type using vector cosine similarity
func (c *Classifier) Classify(text string) ClassificationResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := ClassificationResult{
		SuggestedTags: []int{},
		TagScores:     make(map[int]float64),
	}

	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return res
	}

	docVec := c.vectorizeTokens(tokens)
	docNorm := computeNorm(docVec)
	if docNorm == 0 {
		return res
	}

	// 1. Tag predictions (similarity >= 0.18)
	const tagThreshold = 0.18
	for tid, tVec := range c.tagVecs {
		sim := cosineSimilarity(docVec, docNorm, tVec, c.tagNorms[tid])
		if sim >= tagThreshold {
			res.SuggestedTags = append(res.SuggestedTags, tid)
			res.TagScores[tid] = sim
		}
	}

	// 2. Correspondent prediction (highest similarity >= 0.22)
	const corrThreshold = 0.22
	var bestCorr *int
	bestCorrSim := 0.0
	for cid, cVec := range c.corrVecs {
		sim := cosineSimilarity(docVec, docNorm, cVec, c.corrNorms[cid])
		if sim >= corrThreshold && sim > bestCorrSim {
			bestCorrSim = sim
			id := cid
			bestCorr = &id
		}
	}
	res.SuggestedCorrespondent = bestCorr

	// 3. Document type prediction (highest similarity >= 0.22)
	const docTypeThreshold = 0.22
	var bestDocType *int
	bestDocTypeSim := 0.0
	for dtid, dtVec := range c.docTypeVecs {
		sim := cosineSimilarity(docVec, docNorm, dtVec, c.docTypeNorms[dtid])
		if sim >= docTypeThreshold && sim > bestDocTypeSim {
			bestDocTypeSim = sim
			id := dtid
			bestDocType = &id
		}
	}
	res.SuggestedDocType = bestDocType

	// 4. Storage path prediction (highest similarity >= 0.22)
	const pathThreshold = 0.22
	var bestPath *int
	bestPathSim := 0.0
	for pid, pVec := range c.pathVecs {
		sim := cosineSimilarity(docVec, docNorm, pVec, c.pathNorms[pid])
		if sim >= pathThreshold && sim > bestPathSim {
			bestPathSim = sim
			id := pid
			bestPath = &id
		}
	}
	res.SuggestedStoragePath = bestPath

	return res
}
