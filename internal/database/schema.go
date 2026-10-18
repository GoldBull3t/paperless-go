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
    auth_token TEXT UNIQUE
);

CREATE TABLE IF NOT EXISTS ui_settings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    settings_json TEXT DEFAULT '{}'
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
    created DATETIME NOT NULL,
    modified DATETIME DEFAULT CURRENT_TIMESTAMP,
    added DATETIME DEFAULT CURRENT_TIMESTAMP,
    archive_serial_number INTEGER,
    original_filename TEXT NOT NULL,
    archive_filename TEXT,
    checksum TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    page_count INTEGER DEFAULT 1,
    owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL
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

CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
    title,
    content,
    content='documents',
    content_rowid='id'
);

-- Triggers for FTS5 synchronization
CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
    INSERT INTO documents_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;

CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
END;

CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
    INSERT INTO documents_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;

CREATE INDEX IF NOT EXISTS idx_documents_created ON documents(created);
CREATE INDEX IF NOT EXISTS idx_documents_added ON documents(added);
CREATE INDEX IF NOT EXISTS idx_documents_correspondent ON documents(correspondent_id);
CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(document_type_id);
CREATE INDEX IF NOT EXISTS idx_documents_archive_num ON documents(archive_serial_number);
`
