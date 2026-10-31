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

type Group struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type PermissionsSet struct {
	Users  []int `json:"users"`
	Groups []int `json:"groups"`
}

type ObjectPermissions struct {
	View   PermissionsSet `json:"view"`
	Change PermissionsSet `json:"change"`
}

type Tag struct {
	ID                int                `json:"id"`
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Color             string             `json:"color"`
	TextColor         string             `json:"text_color"`
	Match             string             `json:"match"`
	MatchingAlgorithm int                `json:"matching_algorithm"`
	IsInsensitive     bool               `json:"is_insensitive"`
	IsInboxTag        bool               `json:"is_inbox_tag"`
	Owner             *int               `json:"owner"`
	Permissions       *ObjectPermissions `json:"permissions"`
	UserCanChange     bool               `json:"user_can_change"`
	DocumentCount     int                `json:"document_count"`
}

type Correspondent struct {
	ID                 int                `json:"id"`
	Slug               string             `json:"slug"`
	Name               string             `json:"name"`
	Match              string             `json:"match"`
	MatchingAlgorithm  int                `json:"matching_algorithm"`
	IsInsensitive      bool               `json:"is_insensitive"`
	DocumentCount      int                `json:"document_count"`
	LastCorrespondence *time.Time         `json:"last_correspondence"`
	Owner              *int               `json:"owner"`
	Permissions        *ObjectPermissions `json:"permissions"`
	UserCanChange      bool               `json:"user_can_change"`
}

type DocumentType struct {
	ID                int                `json:"id"`
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Match             string             `json:"match"`
	MatchingAlgorithm int                `json:"matching_algorithm"`
	IsInsensitive     bool               `json:"is_insensitive"`
	DocumentCount     int                `json:"document_count"`
	Owner             *int               `json:"owner"`
	Permissions       *ObjectPermissions `json:"permissions"`
	UserCanChange     bool               `json:"user_can_change"`
}

type StoragePath struct {
	ID                int                `json:"id"`
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Path              string             `json:"path"`
	Match             string             `json:"match"`
	MatchingAlgorithm int                `json:"matching_algorithm"`
	IsInsensitive     bool               `json:"is_insensitive"`
	DocumentCount     int                `json:"document_count"`
	Owner             *int               `json:"owner"`
	Permissions       *ObjectPermissions `json:"permissions"`
	UserCanChange     bool               `json:"user_can_change"`
}

type CustomField struct {
	ID            int                    `json:"id"`
	Name          string                 `json:"name"`
	DataType      string                 `json:"data_type"`
	ExtraData     map[string]interface{} `json:"extra_data"`
	DocumentCount int                    `json:"document_count"`
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

type DocumentVersionInfo struct {
	ID        int       `json:"id"`
	Version   int       `json:"version"`
	Created   time.Time `json:"created"`
	PageCount int       `json:"page_count"`
	Size      int64     `json:"size"`
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
	OriginalFileName    string                     `json:"original_file_name"`
	OriginalFileNameOld string                     `json:"original_filename,omitempty"`
	ArchivedFileName    *string                    `json:"archived_file_name"`
	Owner               *int                       `json:"owner"`
	Permissions         *ObjectPermissions         `json:"permissions"`
	UserCanChange       bool                       `json:"user_can_change"`
	IsSharedByRequester bool                       `json:"is_shared_by_requester"`
	Tags                []int                      `json:"tags"`
	Notes               []DocumentNote             `json:"notes"`
	CustomFields        []DocumentCustomFieldValue `json:"custom_fields"`
	PageCount           int                        `json:"page_count"`
	MimeType            string                     `json:"mime_type"`
	Checksum            string                     `json:"checksum"`
	DuplicateDocuments  []int                      `json:"duplicate_documents"`
	RootDocument        *int                       `json:"root_document"`
	Versions            []DocumentVersionInfo      `json:"versions"`
	ExtractedMetadata   interface{}                `json:"extracted_metadata,omitempty"`
	SearchHit           interface{}                `json:"__search_hit__,omitempty"`
	IsVault             bool                       `json:"is_vault"`
	DeletedAt           *time.Time                 `json:"deleted_at,omitempty"`
}

type SearchHit struct {
	Score          float64  `json:"score"`
	Highlights     []string `json:"highlights"`
	NoteHighlights []string `json:"note_highlights"`
	Rank           int      `json:"rank,omitempty"`
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
	DocumentsInbox         *int                        `json:"documents_inbox"`
	InboxTag               *int                        `json:"inbox_tag"`
	InboxTags              []int                       `json:"inbox_tags"`
	DocumentFileTypeCounts []DocumentFileTypeCountItem `json:"document_file_type_counts"`
	CharacterCount         int                         `json:"character_count"`
	TagCount               int                         `json:"tag_count"`
	CorrespondentCount     int                         `json:"correspondent_count"`
	DocumentTypeCount      int                         `json:"document_type_count"`
	StoragePathCount       int                         `json:"storage_path_count"`
	CurrentASN             *int                        `json:"current_asn"`
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
	ID            int                `json:"id"`
	Name          string             `json:"name"`
	ImapServer    string             `json:"imap_server"`
	ImapPort      int                `json:"imap_port"`
	ImapSecurity  int                `json:"imap_security"`
	Username      string             `json:"username"`
	Password      string             `json:"password"`
	CharacterSet  string             `json:"character_set"`
	IsToken       bool               `json:"is_token"`
	AccountType   int                `json:"account_type"`
	Owner         *int               `json:"owner"`
	UserCanChange bool               `json:"user_can_change"`
	Permissions   *ObjectPermissions `json:"permissions"`
}

type MailRule struct {
	ID                              int                `json:"id"`
	Name                            string             `json:"name"`
	Account                         int                `json:"account"`
	Enabled                         bool               `json:"enabled"`
	Folder                          string             `json:"folder"`
	FilterFrom                      string             `json:"filter_from"`
	FilterTo                        string             `json:"filter_to"`
	FilterSubject                   string             `json:"filter_subject"`
	FilterBody                      string             `json:"filter_body"`
	FilterAttachmentFilenameInclude string             `json:"filter_attachment_filename_include"`
	FilterAttachmentFilenameExclude string             `json:"filter_attachment_filename_exclude"`
	MaximumAge                      int                `json:"maximum_age"`
	Action                          int                `json:"action"`
	ActionParameter                 string             `json:"action_parameter"`
	AssignTitleFrom                 int                `json:"assign_title_from"`
	AssignTags                      []int              `json:"assign_tags"`
	AssignCorrespondentFrom         int                `json:"assign_correspondent_from"`
	AssignCorrespondent             *int               `json:"assign_correspondent"`
	AssignDocumentType              *int               `json:"assign_document_type"`
	AssignOwnerFromRule             bool               `json:"assign_owner_from_rule"`
	Order                           int                `json:"order"`
	AttachmentType                  int                `json:"attachment_type"`
	ConsumptionScope                int                `json:"consumption_scope"`
	PdfLayout                       int                `json:"pdf_layout"`
	StopProcessing                  bool               `json:"stop_processing"`
	Owner                           *int               `json:"owner"`
	UserCanChange                   bool               `json:"user_can_change"`
	Permissions                     *ObjectPermissions `json:"permissions"`
}

type DocumentHistoryEntry struct {
	ID        int                    `json:"id"`
	Actor     map[string]interface{} `json:"actor"`
	Action    string                 `json:"action"`
	Timestamp time.Time              `json:"timestamp"`
	Changes   map[string]interface{} `json:"changes"`
}

type ShareLinkBundle struct {
	ID            int        `json:"id"`
	Slug          string     `json:"slug"`
	Created       time.Time  `json:"created"`
	Expiration    *time.Time `json:"expiration"`
	FileVersion   string     `json:"file_version"`
	Description   string     `json:"description"`
	Documents     []int      `json:"documents"`
	DocumentCount int        `json:"document_count"`
	Owner         *int       `json:"owner"`
}

type WorkflowTrigger struct {
	ID                int    `json:"id"`
	Sources           []int  `json:"sources"`
	Type              int    `json:"type"`
	FilterPath        string `json:"filter_path"`
	FilterFilename    string `json:"filter_filename"`
	FilterMailrule    *int   `json:"filter_mailrule"`
	MatchingAlgorithm int    `json:"matching_algorithm"`
	Match             string `json:"match"`
}

type WorkflowAction struct {
	ID                  int                    `json:"id"`
	Type                int                    `json:"type"`
	AssignTitle         string                 `json:"assign_title"`
	AssignTags          []int                  `json:"assign_tags"`
	AssignCorrespondent *int                   `json:"assign_correspondent"`
	AssignDocumentType  *int                   `json:"assign_document_type"`
	AssignStoragePath   *int                   `json:"assign_storage_path"`
	AssignOwner         *int                   `json:"assign_owner"`
	AssignViewUsers     []int                  `json:"assign_view_users"`
	AssignViewGroups    []int                  `json:"assign_view_groups"`
	AssignChangeUsers   []int                  `json:"assign_change_users"`
	AssignChangeGroups  []int                  `json:"assign_change_groups"`
	AssignCustomFields  []int                  `json:"assign_custom_fields"`
	Email               map[string]interface{} `json:"email"`
	Webhook             map[string]interface{} `json:"webhook"`
}

type ProcessedMail struct {
	ID        int       `json:"id"`
	Account   int       `json:"account"`
	Rule      *int      `json:"rule"`
	Folder    string    `json:"folder"`
	UID       string    `json:"uid"`
	Subject   string    `json:"subject"`
	Received  time.Time `json:"received"`
	Processed time.Time `json:"processed"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	Owner     *int      `json:"owner"`
}
