package database

const SchemaSQL = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    email TEXT DEFAULT '',
    first_name TEXT DEFAULT '',
    last_name TEXT DEFAULT '',
    is_superuser BOOLEAN DEFAULT 0,
    is_staff BOOLEAN DEFAULT 0,
    is_active BOOLEAN DEFAULT 1,
    date_joined DATETIME DEFAULT CURRENT_TIMESTAMP,
    totp_secret TEXT DEFAULT '',
    is_mfa_enabled BOOLEAN DEFAULT 0,
    recovery_codes_json TEXT DEFAULT '[]',
    auth_token TEXT UNIQUE
);

CREATE TABLE IF NOT EXISTS ui_settings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    settings_json TEXT DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS user_groups (
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    group_id INTEGER REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

CREATE TABLE IF NOT EXISTS user_permissions (
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    permission TEXT NOT NULL,
    PRIMARY KEY (user_id, permission)
);

CREATE TABLE IF NOT EXISTS group_permissions (
    group_id INTEGER REFERENCES groups(id) ON DELETE CASCADE,
    permission TEXT NOT NULL,
    PRIMARY KEY (group_id, permission)
);

CREATE TABLE IF NOT EXISTS mail_accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    imap_server TEXT NOT NULL,
    imap_port INTEGER DEFAULT 993,
    imap_security INTEGER DEFAULT 2,
    username TEXT NOT NULL,
    password TEXT NOT NULL,
    character_set TEXT DEFAULT 'UTF-8',
    is_token BOOLEAN DEFAULT 0,
    account_type INTEGER DEFAULT 1,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS mail_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    account_id INTEGER REFERENCES mail_accounts(id) ON DELETE CASCADE,
    enabled BOOLEAN DEFAULT 1,
    folder TEXT DEFAULT 'INBOX',
    filter_from TEXT DEFAULT '',
    filter_to TEXT DEFAULT '',
    filter_subject TEXT DEFAULT '',
    filter_body TEXT DEFAULT '',
    filter_attachment_filename_include TEXT DEFAULT '',
    filter_attachment_filename_exclude TEXT DEFAULT '',
    maximum_age INTEGER DEFAULT 30,
    action INTEGER DEFAULT 1,
    action_parameter TEXT DEFAULT '',
    assign_title_from INTEGER DEFAULT 1,
    assign_tags_json TEXT DEFAULT '[]',
    assign_correspondent_from INTEGER DEFAULT 1,
    assign_correspondent_id INTEGER REFERENCES correspondents(id) ON DELETE SET NULL,
    assign_document_type_id INTEGER REFERENCES document_types(id) ON DELETE SET NULL,
    assign_owner_from_rule BOOLEAN DEFAULT 1,
    order_num INTEGER DEFAULT 0,
    attachment_type INTEGER DEFAULT 1,
    consumption_scope INTEGER DEFAULT 1,
    pdf_layout INTEGER DEFAULT 1,
    stop_processing BOOLEAN DEFAULT 0,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS correspondents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    slug TEXT UNIQUE NOT NULL,
    match TEXT DEFAULT '',
    matching_algorithm INTEGER DEFAULT 0,
    is_insensitive BOOLEAN DEFAULT 1,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    last_correspondence DATETIME
);

CREATE TABLE IF NOT EXISTS tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    slug TEXT UNIQUE NOT NULL,
    color TEXT DEFAULT '#a6cee3',
    text_color TEXT DEFAULT '#000000',
    is_inbox_tag BOOLEAN DEFAULT 0,
    match TEXT DEFAULT '',
    matching_algorithm INTEGER DEFAULT 0,
    is_insensitive BOOLEAN DEFAULT 1,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS document_types (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    slug TEXT UNIQUE NOT NULL,
    match TEXT DEFAULT '',
    matching_algorithm INTEGER DEFAULT 0,
    is_insensitive BOOLEAN DEFAULT 1,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS storage_paths (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    slug TEXT UNIQUE NOT NULL,
    path TEXT DEFAULT '',
    match TEXT DEFAULT '',
    matching_algorithm INTEGER DEFAULT 0,
    is_insensitive BOOLEAN DEFAULT 1,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS custom_fields (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    data_type TEXT NOT NULL,
    extra_data TEXT DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS documents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    content TEXT DEFAULT '',
    correspondent_id INTEGER REFERENCES correspondents(id) ON DELETE SET NULL,
    document_type_id INTEGER REFERENCES document_types(id) ON DELETE SET NULL,
    storage_path_id INTEGER REFERENCES storage_paths(id) ON DELETE SET NULL,
    created DATETIME DEFAULT CURRENT_TIMESTAMP,
    modified DATETIME DEFAULT CURRENT_TIMESTAMP,
    added DATETIME DEFAULT CURRENT_TIMESTAMP,
    archive_serial_number INTEGER,
    original_filename TEXT NOT NULL,
    archive_filename TEXT,
    checksum TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    page_count INTEGER DEFAULT 1,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    root_document_id INTEGER REFERENCES documents(id) ON DELETE SET NULL,
    extracted_metadata_json TEXT DEFAULT '{}',
    is_vault INTEGER DEFAULT 0,
    deleted_at DATETIME
);

CREATE TABLE IF NOT EXISTS document_tags (
    document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
    tag_id INTEGER REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (document_id, tag_id)
);

CREATE TABLE IF NOT EXISTS document_custom_fields (
    document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
    field_id INTEGER REFERENCES custom_fields(id) ON DELETE CASCADE,
    value_text TEXT,
    PRIMARY KEY (document_id, field_id)
);

CREATE TABLE IF NOT EXISTS document_notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
    note TEXT NOT NULL,
    created DATETIME DEFAULT CURRENT_TIMESTAMP,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS saved_views (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    show_on_dashboard BOOLEAN DEFAULT 0,
    show_in_sidebar BOOLEAN DEFAULT 0,
    sort_field TEXT DEFAULT 'created',
    sort_reverse BOOLEAN DEFAULT 1,
    filter_rules TEXT DEFAULT '[]',
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT UNIQUE NOT NULL,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    task_name TEXT NOT NULL,
    task_file_name TEXT,
    date_created DATETIME DEFAULT CURRENT_TIMESTAMP,
    date_done DATETIME,
    result TEXT,
    acknowledged BOOLEAN DEFAULT 0,
    related_document_id INTEGER REFERENCES documents(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS app_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    config_json TEXT DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS share_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created DATETIME DEFAULT CURRENT_TIMESTAMP,
    expiration DATETIME,
    slug TEXT UNIQUE NOT NULL,
    document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
    file_format TEXT DEFAULT 'original'
);

CREATE TABLE IF NOT EXISTS workflows (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    order_num INTEGER DEFAULT 0,
    enabled BOOLEAN DEFAULT 1,
    triggers_json TEXT DEFAULT '[]',
    actions_json TEXT DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS workflow_triggers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workflow_id INTEGER REFERENCES workflows(id) ON DELETE CASCADE,
    type INTEGER DEFAULT 1,
    sources_json TEXT DEFAULT '[]',
    filter_path TEXT DEFAULT '',
    filter_filename TEXT DEFAULT '',
    filter_mailrule_id INTEGER,
    matching_algorithm INTEGER DEFAULT 0,
    match TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS workflow_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workflow_id INTEGER REFERENCES workflows(id) ON DELETE CASCADE,
    type INTEGER DEFAULT 1,
    assign_title TEXT DEFAULT '',
    assign_tags_json TEXT DEFAULT '[]',
    assign_correspondent_id INTEGER REFERENCES correspondents(id) ON DELETE SET NULL,
    assign_document_type_id INTEGER REFERENCES document_types(id) ON DELETE SET NULL,
    assign_storage_path_id INTEGER REFERENCES storage_paths(id) ON DELETE SET NULL,
    assign_owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    assign_view_users_json TEXT DEFAULT '[]',
    assign_view_groups_json TEXT DEFAULT '[]',
    assign_change_users_json TEXT DEFAULT '[]',
    assign_change_groups_json TEXT DEFAULT '[]',
    assign_custom_fields_json TEXT DEFAULT '[]',
    email_json TEXT DEFAULT '{}',
    webhook_json TEXT DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS document_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    action TEXT NOT NULL,
    changes_json TEXT DEFAULT '{}',
    actor_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS share_link_bundles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT UNIQUE NOT NULL,
    description TEXT DEFAULT '',
    expiration DATETIME,
    file_version TEXT DEFAULT 'original',
    created DATETIME DEFAULT CURRENT_TIMESTAMP,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS share_link_bundle_items (
    bundle_id INTEGER REFERENCES share_link_bundles(id) ON DELETE CASCADE,
    document_id INTEGER REFERENCES documents(id) ON DELETE CASCADE,
    PRIMARY KEY (bundle_id, document_id)
);

CREATE TABLE IF NOT EXISTS processed_mail (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER REFERENCES mail_accounts(id) ON DELETE CASCADE,
    rule_id INTEGER REFERENCES mail_rules(id) ON DELETE SET NULL,
    folder TEXT NOT NULL,
    uid TEXT NOT NULL,
    subject TEXT DEFAULT '',
    received DATETIME DEFAULT CURRENT_TIMESTAMP,
    processed DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT DEFAULT 'SUCCESS',
    error TEXT DEFAULT '',
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS vault_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    is_enabled INTEGER DEFAULT 0,
    salt_hex TEXT NOT NULL,
    verifier_hash TEXT NOT NULL,
    auto_lock_minutes INTEGER DEFAULT 15,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS document_embeddings (
    document_id INTEGER PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
    embedding_json TEXT NOT NULL,
    model TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
    title,
    content
);

-- Triggers for FTS5 synchronization
CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
    DELETE FROM documents_fts WHERE rowid = old.id;
END;

CREATE INDEX IF NOT EXISTS idx_documents_created ON documents(created);
CREATE INDEX IF NOT EXISTS idx_documents_added ON documents(added);
CREATE INDEX IF NOT EXISTS idx_documents_correspondent ON documents(correspondent_id);
CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(document_type_id);
CREATE INDEX IF NOT EXISTS idx_documents_archive_num ON documents(archive_serial_number);
CREATE INDEX IF NOT EXISTS idx_documents_deleted ON documents(deleted_at);
`
