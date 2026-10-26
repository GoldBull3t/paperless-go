package models

import "time"

type PaginatedResponse struct {
	Count    int         `json:"count"`
	Next     *string     `json:"next"`
	Previous *string     `json:"previous"`
	All      []int       `json:"all"`
	Results  interface{} `json:"results"`
}

type User struct {
	ID                   int       `json:"id"`
	Username             string    `json:"username"`
	Email                string    `json:"email"`
	FirstName            string    `json:"first_name"`
	LastName             string    `json:"last_name"`
	IsSuperuser          bool      `json:"is_superuser"`
	IsStaff              bool      `json:"is_staff"`
	IsActive             bool      `json:"is_active"`
	DateJoined           time.Time `json:"date_joined"`
	IsMfaEnabled         bool      `json:"is_mfa_enabled"`
	Groups               []int     `json:"groups"`
	UserPermissions      []string  `json:"user_permissions"`
	InheritedPermissions []string  `json:"inherited_permissions"`
	AuthToken            *string   `json:"auth_token,omitempty"`
}

type Tag struct {
	ID                int    `json:"id"`
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	Color             string `json:"color"`
	TextColor         string `json:"text_color"`
	Match             string `json:"match"`
	MatchingAlgorithm int    `json:"matching_algorithm"`
	IsInsensitive     bool   `json:"is_insensitive"`
	IsInboxTag        bool   `json:"is_inbox_tag"`
	Owner             *int   `json:"owner"`
	UserCanChange     bool   `json:"user_can_change"`
	DocumentCount     int    `json:"document_count"`
}

type Correspondent struct {
	ID                 int        `json:"id"`
	Slug               string     `json:"slug"`
	Name               string     `json:"name"`
	Match              string     `json:"match"`
	MatchingAlgorithm  int        `json:"matching_algorithm"`
	IsInsensitive      bool       `json:"is_insensitive"`
	DocumentCount      int        `json:"document_count"`
	LastCorrespondence *time.Time `json:"last_correspondence"`
	Owner              *int       `json:"owner"`
	UserCanChange      bool       `json:"user_can_change"`
}

type DocumentType struct {
	ID                int    `json:"id"`
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	Match             string `json:"match"`
	MatchingAlgorithm int    `json:"matching_algorithm"`
	IsInsensitive     bool   `json:"is_insensitive"`
	DocumentCount     int    `json:"document_count"`
	Owner             *int   `json:"owner"`
	UserCanChange     bool   `json:"user_can_change"`
}

type StoragePath struct {
	ID                int    `json:"id"`
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	Match             string `json:"match"`
	MatchingAlgorithm int    `json:"matching_algorithm"`
	IsInsensitive     bool   `json:"is_insensitive"`
	DocumentCount     int    `json:"document_count"`
	Owner             *int   `json:"owner"`
	UserCanChange     bool   `json:"user_can_change"`
}

type CustomField struct {
	ID        int                    `json:"id"`
	Name      string                 `json:"name"`
	DataType  string                 `json:"data_type"`
	ExtraData map[string]interface{} `json:"extra_data"`
}

type DocumentCustomFieldValue struct {
	Field int         `json:"field"`
	Value interface{} `json:"value"`
}

type DocumentNote struct {
	ID      int       `json:"id"`
	Note    string    `json:"note"`
	Created time.Time `json:"created"`
	User    *int      `json:"user"`
}

type Document struct {
	ID                  int                        `json:"id"`
	Title               string                     `json:"title"`
	Content             string                     `json:"content"`
	Correspondent       *int                       `json:"correspondent"`
	DocumentType        *int                       `json:"document_type"`
	StoragePath         *int                       `json:"storage_path"`
	Created             time.Time                  `json:"created"`
	CreatedDate         string                     `json:"created_date"`
	Modified            time.Time                  `json:"modified"`
	Added               time.Time                  `json:"added"`
	ArchiveSerialNumber *int                       `json:"archive_serial_number"`
	OriginalFileName    string                     `json:"original_filename"`
	ArchivedFileName    *string                    `json:"archived_file_name"`
	Owner               *int                       `json:"owner"`
	UserCanChange       bool                       `json:"user_can_change"`
	IsSharedByRequester bool                       `json:"is_shared_by_requester"`
	Tags                []int                      `json:"tags"`
	Notes               []DocumentNote             `json:"notes"`
	CustomFields        []DocumentCustomFieldValue `json:"custom_fields"`
	PageCount           int                        `json:"page_count"`
	MimeType            string                     `json:"mime_type"`
	ExtractedMetadata   interface{}                `json:"extracted_metadata,omitempty"`
	SearchHit           interface{}                `json:"__search_hit__,omitempty"`
	DeletedAt           *time.Time                 `json:"deleted_at,omitempty"`
}

type SavedViewFilterRule struct {
	RuleType int    `json:"rule_type"`
	Value    string `json:"value"`
}

type SavedView struct {
	ID              int                   `json:"id"`
	Name            string                `json:"name"`
	ShowOnDashboard bool                  `json:"show_on_dashboard"`
	ShowInSidebar   bool                  `json:"show_in_sidebar"`
	SortField       string                `json:"sort_field"`
	SortReverse     bool                  `json:"sort_reverse"`
	FilterRules     []SavedViewFilterRule `json:"filter_rules"`
	Owner           *int                  `json:"owner"`
	UserCanChange   bool                  `json:"user_can_change"`
}

type Task struct {
	ID                   int        `json:"id"`
	TaskID               string     `json:"task_id"`
	Type                 string     `json:"type"`
	Status               string     `json:"status"` // PENDING, STARTED, SUCCESS, FAILURE
	TaskName             string     `json:"task_name"`
	TaskFileName         string     `json:"task_file_name"`
	DateCreated          time.Time  `json:"date_created"`
	DateDone             *time.Time `json:"date_done"`
	Result               *string    `json:"result"`
	Acknowledged         bool       `json:"acknowledged"`
	RelatedDocument      *string    `json:"related_document"`
	RelatedDocumentIDs   []int      `json:"related_document_ids"`
	TriggerSource        string     `json:"trigger_source"`
	TriggerSourceDisplay string     `json:"trigger_source_display"`
}

type DocumentFileTypeCountItem struct {
	MimeType      string `json:"mime_type"`
	MimeTypeCount int    `json:"mime_type_count"`
}

type Statistics struct {
	DocumentsTotal         int                         `json:"documents_total"`
	DocumentsInbox         int                         `json:"documents_inbox"`
	InboxTags              []int                       `json:"inbox_tags"`
	DocumentFileTypeCounts []DocumentFileTypeCountItem `json:"document_file_type_counts"`
	CharacterCount         int                         `json:"character_count"`
}

type SuggestionsResponse struct {
	Tags           []int    `json:"tags"`
	Correspondents []int    `json:"correspondents"`
	DocumentTypes  []int    `json:"document_types"`
	StoragePaths   []int    `json:"storage_paths"`
	Dates          []string `json:"dates"`
}

type SelectionItem struct {
	ID            int `json:"id"`
	DocumentCount int `json:"document_count"`
}

type SelectionDataResponse struct {
	SelectedStoragePaths   []SelectionItem `json:"selected_storage_paths"`
	SelectedCustomFields   []SelectionItem `json:"selected_custom_fields"`
	SelectedCorrespondents []SelectionItem `json:"selected_correspondents"`
	SelectedTags           []SelectionItem `json:"selected_tags"`
	SelectedDocumentTypes  []SelectionItem `json:"selected_document_types"`
}

type ShareLink struct {
	ID            int        `json:"id"`
	Created       time.Time  `json:"created"`
	Expiration    *time.Time `json:"expiration"`
	Slug          string     `json:"slug"`
	Document      int        `json:"document"`
	DocumentTitle string     `json:"document_title,omitempty"`
	FileVersion   string     `json:"file_version"`
	FileFormat    string     `json:"file_format,omitempty"`
}

type Workflow struct {
	ID       int                    `json:"id"`
	Name     string                 `json:"name"`
	Order    int                    `json:"order"`
	Enabled  bool                   `json:"enabled"`
	Triggers []map[string]interface{} `json:"triggers"`
	Actions  []map[string]interface{} `json:"actions"`
}

type MailAccount struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Username string `json:"username"`
}

type MailRule struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Account int    `json:"account"`
	Order   int    `json:"order"`
}
