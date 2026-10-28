package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type MailHandler struct {
	db *database.DB
}

func NewMailHandler(db *database.DB) *MailHandler {
	return &MailHandler{db: db}
}

// Mail Accounts (/api/mail_accounts/)

func (h *MailHandler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, name, imap_server, imap_port, imap_security, username, character_set, is_token, account_type, owner_id
		FROM mail_accounts ORDER BY name ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.MailAccount
	var allIDs []int

	user := GetUserFromContext(r.Context())

	for rows.Next() {
		var a models.MailAccount
		var owner sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Name, &a.ImapServer, &a.ImapPort, &a.ImapSecurity, &a.Username, &a.CharacterSet, &a.IsToken, &a.AccountType, &owner); err == nil {
			if owner.Valid {
				o := int(owner.Int64)
				a.Owner = &o
			}
			a.Password = "**********"
			a.UserCanChange = true
			if user != nil && !user.IsSuperuser {
				a.UserCanChange = (a.Owner == nil || *a.Owner == user.ID)
			}
			a.Permissions = &models.ObjectPermissions{
				View:   models.PermissionsSet{Users: []int{}, Groups: []int{}},
				Change: models.PermissionsSet{Users: []int{}, Groups: []int{}},
			}
			if a.Owner != nil {
				a.Permissions.View.Users = []int{*a.Owner}
				a.Permissions.Change.Users = []int{*a.Owner}
			}
			results = append(results, a)
			allIDs = append(allIDs, a.ID)
		}
	}
	_ = rows.Err()

	if results == nil {
		results = []models.MailAccount{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *MailHandler) GetAccount(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var a models.MailAccount
	var owner sql.NullInt64
	err := h.db.QueryRow(`
		SELECT id, name, imap_server, imap_port, imap_security, username, character_set, is_token, account_type, owner_id
		FROM mail_accounts WHERE id = ?
	`, id).Scan(&a.ID, &a.Name, &a.ImapServer, &a.ImapPort, &a.ImapSecurity, &a.Username, &a.CharacterSet, &a.IsToken, &a.AccountType, &owner)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if owner.Valid {
		o := int(owner.Int64)
		a.Owner = &o
	}
	a.Password = "**********"
	user := GetUserFromContext(r.Context())
	a.UserCanChange = true
	if user != nil && !user.IsSuperuser {
		a.UserCanChange = (a.Owner == nil || *a.Owner == user.ID)
	}
	a.Permissions = &models.ObjectPermissions{
		View:   models.PermissionsSet{Users: []int{}, Groups: []int{}},
		Change: models.PermissionsSet{Users: []int{}, Groups: []int{}},
	}
	if a.Owner != nil {
		a.Permissions.View.Users = []int{*a.Owner}
		a.Permissions.Change.Users = []int{*a.Owner}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}

func (h *MailHandler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name         string `json:"name"`
		ImapServer   string `json:"imap_server"`
		ImapPort     int    `json:"imap_port"`
		ImapSecurity int    `json:"imap_security"`
		Username     string `json:"username"`
		Password     string `json:"password"`
		CharacterSet string `json:"character_set"`
		IsToken      bool   `json:"is_token"`
		AccountType  int    `json:"account_type"`
		Owner        *int   `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		http.Error(w, "Account name is required", http.StatusBadRequest)
		return
	}
	if input.ImapPort == 0 {
		input.ImapPort = 993
	}
	if input.CharacterSet == "" {
		input.CharacterSet = "UTF-8"
	}
	if input.AccountType == 0 {
		input.AccountType = 1
	}

	res, err := h.db.Exec(`
		INSERT INTO mail_accounts (name, imap_server, imap_port, imap_security, username, password, character_set, is_token, account_type, owner_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Name, input.ImapServer, input.ImapPort, input.ImapSecurity, input.Username, input.Password, input.CharacterSet, input.IsToken, input.AccountType, input.Owner)
	if err != nil {
		http.Error(w, "Account name already exists or database error: "+err.Error(), http.StatusBadRequest)
		return
	}

	aid, _ := res.LastInsertId()
	created := models.MailAccount{
		ID:            int(aid),
		Name:          input.Name,
		ImapServer:    input.ImapServer,
		ImapPort:      input.ImapPort,
		ImapSecurity:  input.ImapSecurity,
		Username:      input.Username,
		Password:      "**********",
		CharacterSet:  input.CharacterSet,
		IsToken:       input.IsToken,
		AccountType:   input.AccountType,
		Owner:         input.Owner,
		UserCanChange: true,
		Permissions: &models.ObjectPermissions{
			View:   models.PermissionsSet{Users: []int{}, Groups: []int{}},
			Change: models.PermissionsSet{Users: []int{}, Groups: []int{}},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

func (h *MailHandler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var exists int
	if err := h.db.QueryRow("SELECT id FROM mail_accounts WHERE id = ?", id).Scan(&exists); err != nil {
		http.NotFound(w, r)
		return
	}

	var input struct {
		Name         *string `json:"name"`
		ImapServer   *string `json:"imap_server"`
		ImapPort     *int    `json:"imap_port"`
		ImapSecurity *int    `json:"imap_security"`
		Username     *string `json:"username"`
		Password     *string `json:"password"`
		CharacterSet *string `json:"character_set"`
		IsToken      *bool   `json:"is_token"`
		AccountType  *int    `json:"account_type"`
		Owner        *int    `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Name != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET name = ? WHERE id = ?", *input.Name, id)
	}
	if input.ImapServer != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET imap_server = ? WHERE id = ?", *input.ImapServer, id)
	}
	if input.ImapPort != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET imap_port = ? WHERE id = ?", *input.ImapPort, id)
	}
	if input.ImapSecurity != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET imap_security = ? WHERE id = ?", *input.ImapSecurity, id)
	}
	if input.Username != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET username = ? WHERE id = ?", *input.Username, id)
	}
	if input.Password != nil && strings.Trim(*input.Password, "*") != "" {
		_, _ = h.db.Exec("UPDATE mail_accounts SET password = ? WHERE id = ?", *input.Password, id)
	}
	if input.CharacterSet != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET character_set = ? WHERE id = ?", *input.CharacterSet, id)
	}
	if input.IsToken != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET is_token = ? WHERE id = ?", *input.IsToken, id)
	}
	if input.AccountType != nil {
		_, _ = h.db.Exec("UPDATE mail_accounts SET account_type = ? WHERE id = ?", *input.AccountType, id)
	}
	if input.Owner != nil {
		if *input.Owner <= 0 {
			_, _ = h.db.Exec("UPDATE mail_accounts SET owner_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE mail_accounts SET owner_id = ? WHERE id = ?", *input.Owner, id)
		}
	}

	// Fetch updated
	h.GetAccount(w, r)
}

func (h *MailHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, err := h.db.Exec("DELETE FROM mail_accounts WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Mail Rules (/api/mail_rules/)

func (h *MailHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, name, account_id, enabled, folder, filter_from, filter_to, filter_subject, filter_body,
		       filter_attachment_filename_include, filter_attachment_filename_exclude, maximum_age,
		       action, action_parameter, assign_title_from, assign_tags_json, assign_correspondent_from,
		       assign_correspondent_id, assign_document_type_id, assign_owner_from_rule, order_num,
		       attachment_type, consumption_scope, pdf_layout, stop_processing, owner_id
		FROM mail_rules ORDER BY order_num ASC, name ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.MailRule
	var allIDs []int
	user := GetUserFromContext(r.Context())

	for rows.Next() {
		var mr models.MailRule
		var tagsJSON string
		var corrID, docTypeID, ownerID sql.NullInt64
		err := rows.Scan(
			&mr.ID, &mr.Name, &mr.Account, &mr.Enabled, &mr.Folder, &mr.FilterFrom, &mr.FilterTo, &mr.FilterSubject, &mr.FilterBody,
			&mr.FilterAttachmentFilenameInclude, &mr.FilterAttachmentFilenameExclude, &mr.MaximumAge,
			&mr.Action, &mr.ActionParameter, &mr.AssignTitleFrom, &tagsJSON, &mr.AssignCorrespondentFrom,
			&corrID, &docTypeID, &mr.AssignOwnerFromRule, &mr.Order,
			&mr.AttachmentType, &mr.ConsumptionScope, &mr.PdfLayout, &mr.StopProcessing, &ownerID,
		)
		if err == nil {
			_ = json.Unmarshal([]byte(tagsJSON), &mr.AssignTags)
			if mr.AssignTags == nil {
				mr.AssignTags = []int{}
			}
			if corrID.Valid {
				c := int(corrID.Int64)
				mr.AssignCorrespondent = &c
			}
			if docTypeID.Valid {
				dt := int(docTypeID.Int64)
				mr.AssignDocumentType = &dt
			}
			if ownerID.Valid {
				o := int(ownerID.Int64)
				mr.Owner = &o
			}
			mr.UserCanChange = true
			if user != nil && !user.IsSuperuser {
				mr.UserCanChange = (mr.Owner == nil || *mr.Owner == user.ID)
			}
			mr.Permissions = &models.ObjectPermissions{
				View:   models.PermissionsSet{Users: []int{}, Groups: []int{}},
				Change: models.PermissionsSet{Users: []int{}, Groups: []int{}},
			}
			if mr.Owner != nil {
				mr.Permissions.View.Users = []int{*mr.Owner}
				mr.Permissions.Change.Users = []int{*mr.Owner}
			}
			results = append(results, mr)
			allIDs = append(allIDs, mr.ID)
		}
	}
	_ = rows.Err()

	if results == nil {
		results = []models.MailRule{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *MailHandler) GetRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var mr models.MailRule
	var tagsJSON string
	var corrID, docTypeID, ownerID sql.NullInt64
	err := h.db.QueryRow(`
		SELECT id, name, account_id, enabled, folder, filter_from, filter_to, filter_subject, filter_body,
		       filter_attachment_filename_include, filter_attachment_filename_exclude, maximum_age,
		       action, action_parameter, assign_title_from, assign_tags_json, assign_correspondent_from,
		       assign_correspondent_id, assign_document_type_id, assign_owner_from_rule, order_num,
		       attachment_type, consumption_scope, pdf_layout, stop_processing, owner_id
		FROM mail_rules WHERE id = ?
	`, id).Scan(
		&mr.ID, &mr.Name, &mr.Account, &mr.Enabled, &mr.Folder, &mr.FilterFrom, &mr.FilterTo, &mr.FilterSubject, &mr.FilterBody,
		&mr.FilterAttachmentFilenameInclude, &mr.FilterAttachmentFilenameExclude, &mr.MaximumAge,
		&mr.Action, &mr.ActionParameter, &mr.AssignTitleFrom, &tagsJSON, &mr.AssignCorrespondentFrom,
		&corrID, &docTypeID, &mr.AssignOwnerFromRule, &mr.Order,
		&mr.AttachmentType, &mr.ConsumptionScope, &mr.PdfLayout, &mr.StopProcessing, &ownerID,
	)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.Unmarshal([]byte(tagsJSON), &mr.AssignTags)
	if mr.AssignTags == nil {
		mr.AssignTags = []int{}
	}
	if corrID.Valid {
		c := int(corrID.Int64)
		mr.AssignCorrespondent = &c
	}
	if docTypeID.Valid {
		dt := int(docTypeID.Int64)
		mr.AssignDocumentType = &dt
	}
	if ownerID.Valid {
		o := int(ownerID.Int64)
		mr.Owner = &o
	}
	user := GetUserFromContext(r.Context())
	mr.UserCanChange = true
	if user != nil && !user.IsSuperuser {
		mr.UserCanChange = (mr.Owner == nil || *mr.Owner == user.ID)
	}
	mr.Permissions = &models.ObjectPermissions{
		View:   models.PermissionsSet{Users: []int{}, Groups: []int{}},
		Change: models.PermissionsSet{Users: []int{}, Groups: []int{}},
	}
	if mr.Owner != nil {
		mr.Permissions.View.Users = []int{*mr.Owner}
		mr.Permissions.Change.Users = []int{*mr.Owner}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(mr)
}

func (h *MailHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name                            string `json:"name"`
		Account                         int    `json:"account"`
		Enabled                         bool   `json:"enabled"`
		Folder                          string `json:"folder"`
		FilterFrom                      string `json:"filter_from"`
		FilterTo                        string `json:"filter_to"`
		FilterSubject                   string `json:"filter_subject"`
		FilterBody                      string `json:"filter_body"`
		FilterAttachmentFilenameInclude string `json:"filter_attachment_filename_include"`
		FilterAttachmentFilenameExclude string `json:"filter_attachment_filename_exclude"`
		MaximumAge                      int    `json:"maximum_age"`
		Action                          int    `json:"action"`
		ActionParameter                 string `json:"action_parameter"`
		AssignTitleFrom                 int    `json:"assign_title_from"`
		AssignTags                      []int  `json:"assign_tags"`
		AssignCorrespondentFrom         int    `json:"assign_correspondent_from"`
		AssignCorrespondent             *int   `json:"assign_correspondent"`
		AssignDocumentType              *int   `json:"assign_document_type"`
		AssignOwnerFromRule             bool   `json:"assign_owner_from_rule"`
		Order                           int    `json:"order"`
		AttachmentType                  int    `json:"attachment_type"`
		ConsumptionScope                int    `json:"consumption_scope"`
		PdfLayout                       int    `json:"pdf_layout"`
		StopProcessing                  bool   `json:"stop_processing"`
		Owner                           *int   `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		http.Error(w, "Rule name is required", http.StatusBadRequest)
		return
	}
	if input.Folder == "" {
		input.Folder = "INBOX"
	}
	if input.MaximumAge == 0 {
		input.MaximumAge = 30
	}
	if input.AssignTags == nil {
		input.AssignTags = []int{}
	}
	tagsJSON, _ := json.Marshal(input.AssignTags)

	res, err := h.db.Exec(`
		INSERT INTO mail_rules (
			name, account_id, enabled, folder, filter_from, filter_to, filter_subject, filter_body,
			filter_attachment_filename_include, filter_attachment_filename_exclude, maximum_age,
			action, action_parameter, assign_title_from, assign_tags_json, assign_correspondent_from,
			assign_correspondent_id, assign_document_type_id, assign_owner_from_rule, order_num,
			attachment_type, consumption_scope, pdf_layout, stop_processing, owner_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Name, input.Account, input.Enabled, input.Folder, input.FilterFrom, input.FilterTo, input.FilterSubject, input.FilterBody,
		input.FilterAttachmentFilenameInclude, input.FilterAttachmentFilenameExclude, input.MaximumAge,
		input.Action, input.ActionParameter, input.AssignTitleFrom, string(tagsJSON), input.AssignCorrespondentFrom,
		input.AssignCorrespondent, input.AssignDocumentType, input.AssignOwnerFromRule, input.Order,
		input.AttachmentType, input.ConsumptionScope, input.PdfLayout, input.StopProcessing, input.Owner)
	if err != nil {
		http.Error(w, "Failed to create rule: "+err.Error(), http.StatusBadRequest)
		return
	}

	rid, _ := res.LastInsertId()
	created := models.MailRule{
		ID:                              int(rid),
		Name:                            input.Name,
		Account:                         input.Account,
		Enabled:                         input.Enabled,
		Folder:                          input.Folder,
		FilterFrom:                      input.FilterFrom,
		FilterTo:                        input.FilterTo,
		FilterSubject:                   input.FilterSubject,
		FilterBody:                      input.FilterBody,
		FilterAttachmentFilenameInclude: input.FilterAttachmentFilenameInclude,
		FilterAttachmentFilenameExclude: input.FilterAttachmentFilenameExclude,
		MaximumAge:                      input.MaximumAge,
		Action:                          input.Action,
		ActionParameter:                 input.ActionParameter,
		AssignTitleFrom:                 input.AssignTitleFrom,
		AssignTags:                      input.AssignTags,
		AssignCorrespondentFrom:         input.AssignCorrespondentFrom,
		AssignCorrespondent:             input.AssignCorrespondent,
		AssignDocumentType:              input.AssignDocumentType,
		AssignOwnerFromRule:             input.AssignOwnerFromRule,
		Order:                           input.Order,
		AttachmentType:                  input.AttachmentType,
		ConsumptionScope:                input.ConsumptionScope,
		PdfLayout:                       input.PdfLayout,
		StopProcessing:                  input.StopProcessing,
		Owner:                           input.Owner,
		UserCanChange:                   true,
		Permissions: &models.ObjectPermissions{
			View:   models.PermissionsSet{Users: []int{}, Groups: []int{}},
			Change: models.PermissionsSet{Users: []int{}, Groups: []int{}},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

func (h *MailHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var exists int
	if err := h.db.QueryRow("SELECT id FROM mail_rules WHERE id = ?", id).Scan(&exists); err != nil {
		http.NotFound(w, r)
		return
	}

	var input struct {
		Name                            *string `json:"name"`
		Account                         *int    `json:"account"`
		Enabled                         *bool   `json:"enabled"`
		Folder                          *string `json:"folder"`
		FilterFrom                      *string `json:"filter_from"`
		FilterTo                        *string `json:"filter_to"`
		FilterSubject                   *string `json:"filter_subject"`
		FilterBody                      *string `json:"filter_body"`
		FilterAttachmentFilenameInclude *string `json:"filter_attachment_filename_include"`
		FilterAttachmentFilenameExclude *string `json:"filter_attachment_filename_exclude"`
		MaximumAge                      *int    `json:"maximum_age"`
		Action                          *int    `json:"action"`
		ActionParameter                 *string `json:"action_parameter"`
		AssignTitleFrom                 *int    `json:"assign_title_from"`
		AssignTags                      *[]int  `json:"assign_tags"`
		AssignCorrespondentFrom         *int    `json:"assign_correspondent_from"`
		AssignCorrespondent             *int    `json:"assign_correspondent"`
		AssignDocumentType              *int    `json:"assign_document_type"`
		AssignOwnerFromRule             *bool   `json:"assign_owner_from_rule"`
		Order                           *int    `json:"order"`
		AttachmentType                  *int    `json:"attachment_type"`
		ConsumptionScope                *int    `json:"consumption_scope"`
		PdfLayout                       *int    `json:"pdf_layout"`
		StopProcessing                  *bool   `json:"stop_processing"`
		Owner                           *int    `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Name != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET name = ? WHERE id = ?", *input.Name, id)
	}
	if input.Account != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET account_id = ? WHERE id = ?", *input.Account, id)
	}
	if input.Enabled != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET enabled = ? WHERE id = ?", *input.Enabled, id)
	}
	if input.Folder != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET folder = ? WHERE id = ?", *input.Folder, id)
	}
	if input.FilterFrom != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET filter_from = ? WHERE id = ?", *input.FilterFrom, id)
	}
	if input.FilterTo != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET filter_to = ? WHERE id = ?", *input.FilterTo, id)
	}
	if input.FilterSubject != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET filter_subject = ? WHERE id = ?", *input.FilterSubject, id)
	}
	if input.FilterBody != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET filter_body = ? WHERE id = ?", *input.FilterBody, id)
	}
	if input.FilterAttachmentFilenameInclude != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET filter_attachment_filename_include = ? WHERE id = ?", *input.FilterAttachmentFilenameInclude, id)
	}
	if input.FilterAttachmentFilenameExclude != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET filter_attachment_filename_exclude = ? WHERE id = ?", *input.FilterAttachmentFilenameExclude, id)
	}
	if input.MaximumAge != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET maximum_age = ? WHERE id = ?", *input.MaximumAge, id)
	}
	if input.Action != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET action = ? WHERE id = ?", *input.Action, id)
	}
	if input.ActionParameter != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET action_parameter = ? WHERE id = ?", *input.ActionParameter, id)
	}
	if input.AssignTitleFrom != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET assign_title_from = ? WHERE id = ?", *input.AssignTitleFrom, id)
	}
	if input.AssignTags != nil {
		tagsJSON, _ := json.Marshal(*input.AssignTags)
		_, _ = h.db.Exec("UPDATE mail_rules SET assign_tags_json = ? WHERE id = ?", string(tagsJSON), id)
	}
	if input.AssignCorrespondentFrom != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET assign_correspondent_from = ? WHERE id = ?", *input.AssignCorrespondentFrom, id)
	}
	if input.AssignCorrespondent != nil {
		if *input.AssignCorrespondent <= 0 {
			_, _ = h.db.Exec("UPDATE mail_rules SET assign_correspondent_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE mail_rules SET assign_correspondent_id = ? WHERE id = ?", *input.AssignCorrespondent, id)
		}
	}
	if input.AssignDocumentType != nil {
		if *input.AssignDocumentType <= 0 {
			_, _ = h.db.Exec("UPDATE mail_rules SET assign_document_type_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE mail_rules SET assign_document_type_id = ? WHERE id = ?", *input.AssignDocumentType, id)
		}
	}
	if input.AssignOwnerFromRule != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET assign_owner_from_rule = ? WHERE id = ?", *input.AssignOwnerFromRule, id)
	}
	if input.Order != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET order_num = ? WHERE id = ?", *input.Order, id)
	}
	if input.AttachmentType != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET attachment_type = ? WHERE id = ?", *input.AttachmentType, id)
	}
	if input.ConsumptionScope != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET consumption_scope = ? WHERE id = ?", *input.ConsumptionScope, id)
	}
	if input.PdfLayout != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET pdf_layout = ? WHERE id = ?", *input.PdfLayout, id)
	}
	if input.StopProcessing != nil {
		_, _ = h.db.Exec("UPDATE mail_rules SET stop_processing = ? WHERE id = ?", *input.StopProcessing, id)
	}
	if input.Owner != nil {
		if *input.Owner <= 0 {
			_, _ = h.db.Exec("UPDATE mail_rules SET owner_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE mail_rules SET owner_id = ? WHERE id = ?", *input.Owner, id)
		}
	}

	h.GetRule(w, r)
}

func (h *MailHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, err := h.db.Exec("DELETE FROM mail_rules WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MailHandler) ListProcessedMail(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, account_id, rule_id, folder, uid, subject, received, processed, status, error, owner_id
		FROM processed_mail
		ORDER BY processed DESC
	`)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.PaginatedResponse{Count: 0, Results: []models.ProcessedMail{}})
		return
	}
	defer rows.Close()

	var results []models.ProcessedMail
	var allIDs []int
	for rows.Next() {
		var m models.ProcessedMail
		var rule, owner sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Account, &rule, &m.Folder, &m.UID, &m.Subject, &m.Received, &m.Processed, &m.Status, &m.Error, &owner); err == nil {
			if rule.Valid {
				v := int(rule.Int64)
				m.Rule = &v
			}
			if owner.Valid {
				v := int(owner.Int64)
				m.Owner = &v
			}
			results = append(results, m)
			allIDs = append(allIDs, m.ID)
		}
	}
	if results == nil {
		results = []models.ProcessedMail{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *MailHandler) DeleteProcessedMail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, _ = h.db.Exec("DELETE FROM processed_mail WHERE id = ?", id)
	w.WriteHeader(http.StatusNoContent)
}

