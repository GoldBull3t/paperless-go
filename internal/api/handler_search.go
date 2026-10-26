package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"paperless-go/internal/ai"
	"paperless-go/internal/compress"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type SearchHandler struct {
	db       *database.DB
	consumer *consumer.Consumer
}

func NewSearchHandler(db *database.DB, consumer *consumer.Consumer) *SearchHandler {
	return &SearchHandler{db: db, consumer: consumer}
}

func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	likeQuery := "%" + query + "%"
	user := GetUserFromContext(r.Context())

	// 1. Documents (up to 10)
	var docs []models.Document
	if query != "" {
		// Hybrid Semantic Vector Search (Feature 1. C)
		var semanticDocIDs []int
		if h.consumer != nil && h.consumer.GetAIProvider() != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			qEmbs, err := h.consumer.GetAIProvider().Embed(ctx, []string{query})
			if err == nil && len(qEmbs) > 0 {
				qVec := qEmbs[0]
				embRows, err := h.db.Query("SELECT document_id, embedding_json FROM document_embeddings")
				if err == nil {
					defer embRows.Close()
					type scoredDoc struct {
						id    int
						score float32
					}
					var scored []scoredDoc
					for embRows.Next() {
						var docID int
						var rawEmb string
						if err := embRows.Scan(&docID, &rawEmb); err == nil {
							if docVec, err := ai.DecodeEmbedding(rawEmb); err == nil {
								sim := ai.CosineSimilarity(qVec, docVec)
								if sim > 0.40 {
									scored = append(scored, scoredDoc{id: docID, score: sim})
								}
							}
						}
					}
					sort.Slice(scored, func(i, j int) bool {
						return scored[i].score > scored[j].score
					})
					for i := 0; i < len(scored) && i < 10; i++ {
						semanticDocIDs = append(semanticDocIDs, scored[i].id)
					}
				}
			}
		}

		safeFTS := sanitizeFTSQuery(query)
		var docRows *sql.Rows
		var err error

		docOwnerFilter := ""
		var docArgs []interface{}
		if user != nil && !user.IsSuperuser {
			docOwnerFilter = " AND (d.owner_id IS NULL OR d.owner_id = ?)"
		}

		semanticClause := ""
		if len(semanticDocIDs) > 0 {
			var idsStr []string
			for _, id := range semanticDocIDs {
				idsStr = append(idsStr, strconv.Itoa(id))
			}
			semanticClause = fmt.Sprintf(" OR d.id IN (%s)", strings.Join(idsStr, ","))
		}

		if safeFTS != "" {
			docArgs = append(docArgs, safeFTS, likeQuery)
			if user != nil && !user.IsSuperuser {
				docArgs = append(docArgs, user.ID)
			}
			docRows, err = h.db.Query(fmt.Sprintf(`
				SELECT d.id, d.title, d.content, d.correspondent_id, d.document_type_id, d.storage_path_id,
				       d.created, d.modified, d.added, d.archive_serial_number, d.original_filename, d.archive_filename,
				       d.mime_type, d.owner_id, d.page_count
				FROM documents d
				WHERE d.deleted_at IS NULL AND (d.id IN (SELECT rowid FROM documents_fts WHERE documents_fts MATCH ?) OR d.title LIKE ?%s)%s
				ORDER BY d.created DESC LIMIT 10
			`, semanticClause, docOwnerFilter), docArgs...)
		} else {
			docArgs = append(docArgs, likeQuery, likeQuery)
			if user != nil && !user.IsSuperuser {
				docArgs = append(docArgs, user.ID)
			}
			docRows, err = h.db.Query(fmt.Sprintf(`
				SELECT d.id, d.title, d.content, d.correspondent_id, d.document_type_id, d.storage_path_id,
				       d.created, d.modified, d.added, d.archive_serial_number, d.original_filename, d.archive_filename,
				       d.mime_type, d.owner_id, d.page_count
				FROM documents d
				WHERE d.deleted_at IS NULL AND (d.title LIKE ? OR d.content LIKE ?%s)%s
				ORDER BY d.created DESC LIMIT 10
			`, semanticClause, docOwnerFilter), docArgs...)
		}

		if err == nil {
			defer docRows.Close()
			for docRows.Next() {
				var doc models.Document
				var corr, docType, storPath, owner, asn sql.NullInt64
				var archFile sql.NullString
				if err := docRows.Scan(
					&doc.ID, &doc.Title, &doc.Content, &corr, &docType, &storPath,
					&doc.Created, &doc.Modified, &doc.Added, &asn, &doc.OriginalFileName, &archFile,
					&doc.MimeType, &owner, &doc.PageCount,
				); err == nil {
					if corr.Valid {
						c := int(corr.Int64)
						doc.Correspondent = &c
					}
					if docType.Valid {
						dt := int(docType.Int64)
						doc.DocumentType = &dt
					}
					if storPath.Valid {
						sp := int(storPath.Int64)
						doc.StoragePath = &sp
					}
					if owner.Valid {
						o := int(owner.Int64)
						doc.Owner = &o
					}
					if asn.Valid {
						a := int(asn.Int64)
						doc.ArchiveSerialNumber = &a
					}
					if archFile.Valid {
						doc.ArchivedFileName = &archFile.String
					}
					doc.Content = compress.DecompressText(doc.Content)
					if len(doc.Content) > 100 {
						doc.Content = doc.Content[:100] + "..."
					}
					doc.CreatedDate = doc.Created.Format("2006-01-02")
					doc.UserCanChange = (user == nil || user.IsSuperuser || doc.Owner == nil || *doc.Owner == user.ID)
					doc.IsSharedByRequester = (user != nil && doc.Owner != nil && *doc.Owner == user.ID)
					doc.Notes = []models.DocumentNote{}
					doc.CustomFields = []models.DocumentCustomFieldValue{}
					doc.Tags = []int{}
					docs = append(docs, doc)
				}
			}
		}
	}
	if docs == nil {
		docs = []models.Document{}
	}

	ownerFilter := ""
	var ownerArgs []interface{}
	if user != nil && !user.IsSuperuser {
		ownerFilter = " AND (owner_id IS NULL OR owner_id = ?)"
		ownerArgs = append(ownerArgs, user.ID)
	}

	// 2. Saved views (up to 5)
	var views []models.SavedView
	if query != "" {
		svArgs := append([]interface{}{likeQuery}, ownerArgs...)
		rows, err := h.db.Query(fmt.Sprintf(`
			SELECT id, name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id
			FROM saved_views WHERE name LIKE ?%s ORDER BY name ASC LIMIT 5
		`, ownerFilter), svArgs...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var sv models.SavedView
				var owner sql.NullInt64
				var rulesJSON string
				if err := rows.Scan(&sv.ID, &sv.Name, &sv.ShowOnDashboard, &sv.ShowInSidebar, &sv.SortField, &sv.SortReverse, &rulesJSON, &owner); err == nil {
					if owner.Valid {
						o := int(owner.Int64)
						sv.Owner = &o
					}
					sv.UserCanChange = (user == nil || user.IsSuperuser || sv.Owner == nil || *sv.Owner == user.ID)
					_ = json.Unmarshal([]byte(rulesJSON), &sv.FilterRules)
					if sv.FilterRules == nil {
						sv.FilterRules = []models.SavedViewFilterRule{}
					}
					views = append(views, sv)
				}
			}
			_ = rows.Err()
		}
	}
	if views == nil {
		views = []models.SavedView{}
	}

	// 3. Tags (up to 5)
	var tags []models.Tag
	if query != "" {
		tagArgs := append([]interface{}{likeQuery}, ownerArgs...)
		rows, err := h.db.Query(fmt.Sprintf(`
			SELECT id, slug, name, color, text_color, match, matching_algorithm, is_insensitive, is_inbox_tag, owner_id
			FROM tags WHERE name LIKE ?%s ORDER BY name ASC LIMIT 5
		`, ownerFilter), tagArgs...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var t models.Tag
				var owner sql.NullInt64
				if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Color, &t.TextColor, &t.Match, &t.MatchingAlgorithm, &t.IsInsensitive, &t.IsInboxTag, &owner); err == nil {
					if owner.Valid {
						o := int(owner.Int64)
						t.Owner = &o
					}
					t.UserCanChange = (user == nil || user.IsSuperuser || t.Owner == nil || *t.Owner == user.ID)
					tags = append(tags, t)
				}
			}
			_ = rows.Err()
		}
	}
	if tags == nil {
		tags = []models.Tag{}
	}

	// 4. Correspondents (up to 5)
	var corrs []models.Correspondent
	if query != "" {
		corrArgs := append([]interface{}{likeQuery}, ownerArgs...)
		rows, err := h.db.Query(fmt.Sprintf(`
			SELECT id, slug, name, match, matching_algorithm, is_insensitive, owner_id
			FROM correspondents WHERE name LIKE ?%s ORDER BY name ASC LIMIT 5
		`, ownerFilter), corrArgs...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var c models.Correspondent
				var owner sql.NullInt64
				if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Match, &c.MatchingAlgorithm, &c.IsInsensitive, &owner); err == nil {
					if owner.Valid {
						o := int(owner.Int64)
						c.Owner = &o
					}
					c.UserCanChange = (user == nil || user.IsSuperuser || c.Owner == nil || *c.Owner == user.ID)
					corrs = append(corrs, c)
				}
			}
			_ = rows.Err()
		}
	}
	if corrs == nil {
		corrs = []models.Correspondent{}
	}

	// 5. Document types (up to 5)
	var dts []models.DocumentType
	if query != "" {
		dtArgs := append([]interface{}{likeQuery}, ownerArgs...)
		rows, err := h.db.Query(fmt.Sprintf(`
			SELECT id, slug, name, match, matching_algorithm, is_insensitive, owner_id
			FROM document_types WHERE name LIKE ?%s ORDER BY name ASC LIMIT 5
		`, ownerFilter), dtArgs...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var dt models.DocumentType
				var owner sql.NullInt64
				if err := rows.Scan(&dt.ID, &dt.Slug, &dt.Name, &dt.Match, &dt.MatchingAlgorithm, &dt.IsInsensitive, &owner); err == nil {
					if owner.Valid {
						o := int(owner.Int64)
						dt.Owner = &o
					}
					dt.UserCanChange = (user == nil || user.IsSuperuser || dt.Owner == nil || *dt.Owner == user.ID)
					dts = append(dts, dt)
				}
			}
			_ = rows.Err()
		}
	}
	if dts == nil {
		dts = []models.DocumentType{}
	}

	// 6. Workflows (up to 5)
	var wfs []models.Workflow
	if query != "" {
		rows, err := h.db.Query(`
			SELECT id, name, order_num, enabled, triggers_json, actions_json
			FROM workflows WHERE name LIKE ? ORDER BY name ASC LIMIT 5
		`, likeQuery)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var wf models.Workflow
				var trigsJSON, actsJSON string
				if err := rows.Scan(&wf.ID, &wf.Name, &wf.Order, &wf.Enabled, &trigsJSON, &actsJSON); err == nil {
					_ = json.Unmarshal([]byte(trigsJSON), &wf.Triggers)
					if wf.Triggers == nil {
						wf.Triggers = []map[string]interface{}{}
					}
					_ = json.Unmarshal([]byte(actsJSON), &wf.Actions)
					if wf.Actions == nil {
						wf.Actions = []map[string]interface{}{}
					}
					wfs = append(wfs, wf)
				}
			}
			_ = rows.Err()
		}
	}
	if wfs == nil {
		wfs = []models.Workflow{}
	}

	resp := map[string]interface{}{
		"documents":      docs,
		"saved_views":    views,
		"tags":           tags,
		"correspondents": corrs,
		"document_types": dts,
		"workflows":      wfs,
		"mail_rules":     []interface{}{},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *SearchHandler) Autocomplete(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	var suggestions []string

	if len(term) >= 2 {
		likeTerm := "%" + term + "%"
		user := GetUserFromContext(r.Context())
		docOwnerFilter := ""
		var docArgs []interface{}
		docArgs = append(docArgs, likeTerm)
		if user != nil && !user.IsSuperuser {
			docOwnerFilter = " AND (owner_id IS NULL OR owner_id = ?)"
			docArgs = append(docArgs, user.ID)
		}

		rows, err := h.db.Query(fmt.Sprintf(`
			SELECT DISTINCT title FROM documents WHERE deleted_at IS NULL AND title LIKE ?%s LIMIT 5
		`, docOwnerFilter), docArgs...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var t string
				if err := rows.Scan(&t); err == nil {
					suggestions = append(suggestions, t)
				}
			}
			_ = rows.Err()
		}

		tagOwnerFilter := ""
		var tagArgs []interface{}
		tagArgs = append(tagArgs, likeTerm)
		if user != nil && !user.IsSuperuser {
			tagOwnerFilter = " AND (owner_id IS NULL OR owner_id = ?)"
			tagArgs = append(tagArgs, user.ID)
		}

		rowsT, err := h.db.Query(fmt.Sprintf(`SELECT name FROM tags WHERE name LIKE ?%s LIMIT 3`, tagOwnerFilter), tagArgs...)
		if err == nil {
			defer rowsT.Close()
			for rowsT.Next() {
				var name string
				if err := rowsT.Scan(&name); err == nil {
					suggestions = append(suggestions, name)
				}
			}
			_ = rowsT.Err()
		}
	}

	if suggestions == nil {
		suggestions = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(suggestions)
}
