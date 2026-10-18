package api

import (
	"encoding/json"
	"net/http"
)

type SchemaHandler struct{}

func NewSchemaHandler() *SchemaHandler {
	return &SchemaHandler{}
}

// GetSchema returns the OpenAPI 3.0 specification for Paperless-go
func (h *SchemaHandler) GetSchema(w http.ResponseWriter, r *http.Request) {
	spec := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":       "Paperless-ngx API (Go High-Performance Backend)",
			"version":     "2.14.0",
			"description": "High-performance, zero-Cgo Paperless-ngx REST API implemented in Go with full SQLite, eSCL scanning, and workflow support.",
		},
		"paths": map[string]interface{}{
			"/api/documents/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Documents",
					"tags":    []string{"Documents"},
					"parameters": []map[string]interface{}{
						{"name": "page", "in": "query", "schema": map[string]string{"type": "integer"}},
						{"name": "page_size", "in": "query", "schema": map[string]string{"type": "integer"}},
						{"name": "query", "in": "query", "schema": map[string]string{"type": "string"}},
						{"name": "ordering", "in": "query", "schema": map[string]string{"type": "string"}},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Paginated list of documents"},
					},
				},
				"post": map[string]interface{}{
					"summary": "Upload & Ingest Document",
					"tags":    []string{"Documents"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Document task UUID queued for processing"},
					},
				},
			},
			"/api/documents/{id}/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "Get Document Details",
					"tags":    []string{"Documents"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Document object"},
					},
				},
				"put": map[string]interface{}{
					"summary": "Update Document Metadata",
					"tags":    []string{"Documents"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Updated document object"},
					},
				},
				"delete": map[string]interface{}{
					"summary": "Move Document to Trash / Delete",
					"tags":    []string{"Documents"},
					"responses": map[string]interface{}{
						"204": map[string]interface{}{"description": "Document deleted"},
					},
				},
			},
			"/api/tags/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Tags",
					"tags":    []string{"Tags"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Tags list"},
					},
				},
			},
			"/api/correspondents/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Correspondents",
					"tags":    []string{"Correspondents"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Correspondents list"},
					},
				},
			},
			"/api/document_types/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Document Types",
					"tags":    []string{"Document Types"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Document types list"},
					},
				},
			},
			"/api/storage_paths/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Storage Paths",
					"tags":    []string{"Storage Paths"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Storage paths list"},
					},
				},
			},
			"/api/custom_fields/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Custom Fields",
					"tags":    []string{"Custom Fields"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Custom fields list"},
					},
				},
			},
			"/api/saved_views/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Saved Views",
					"tags":    []string{"Saved Views"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Saved views list"},
					},
				},
			},
			"/api/tasks/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Tasks & Background Jobs",
					"tags":    []string{"Tasks"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Tasks list"},
					},
				},
			},
			"/api/scan/scanners/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Available Network Scanners (eSCL)",
					"tags":    []string{"Scan"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "eSCL Scanner list"},
					},
				},
			},
			"/api/scan/scan/": map[string]interface{}{
				"post": map[string]interface{}{
					"summary": "Trigger Scan Job",
					"tags":    []string{"Scan"},
					"responses": map[string]interface{}{
						"202": map[string]interface{}{"description": "Scan task queued"},
					},
				},
			},
			"/api/share_links/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List Share Links",
					"tags":    []string{"Share Links"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Share links list"},
					},
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(spec)
}

// SwaggerUI renders the interactive Swagger UI documentation page
func (h *SchemaHandler) SwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<title>Paperless-ngx API Documentation - Swagger UI</title>
	<link rel="icon" type="image/x-icon" href="/static/paperless/img/favicon.ico">
	<link rel="stylesheet" type="text/css" href="/static/drf_spectacular_sidecar/swagger-ui-dist/swagger-ui.css">
	<style>
		html { box-sizing: border-box; overflow: -moz-scrollbars-vertical; overflow-y: scroll; }
		*, *:before, *:after { box-sizing: inherit; }
		body { margin: 0; background: #fafafa; }
		.swagger-ui .topbar { background-color: #17541f; }
	</style>
</head>
<body>
	<div id="swagger-ui"></div>
	<script src="/static/drf_spectacular_sidecar/swagger-ui-dist/swagger-ui-bundle.js"></script>
	<script src="/static/drf_spectacular_sidecar/swagger-ui-dist/swagger-ui-standalone-preset.js"></script>
	<script>
		window.onload = function() {
			window.ui = SwaggerUIBundle({
				url: "/api/schema/",
				dom_id: '#swagger-ui',
				deepLinking: true,
				presets: [
					SwaggerUIBundle.presets.apis,
					SwaggerUIStandalonePreset
				],
				plugins: [
					SwaggerUIBundle.plugins.DownloadUrl
				],
				layout: "StandaloneLayout"
			});
		};
	</script>
</body>
</html>`
	_, _ = w.Write([]byte(html))
}

// Redoc renders the clean ReDoc documentation page
func (h *SchemaHandler) Redoc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `<!DOCTYPE html>
<html>
<head>
	<title>Paperless-ngx API Documentation - ReDoc</title>
	<meta charset="utf-8"/>
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<link rel="icon" type="image/x-icon" href="/static/paperless/img/favicon.ico">
	<link href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700" rel="stylesheet">
	<style>body { margin: 0; padding: 0; }</style>
</head>
<body>
	<redoc spec-url='/api/schema/'></redoc>
	<script src="/static/drf_spectacular_sidecar/redoc/bundles/redoc.standalone.js"></script>
</body>
</html>`
	_, _ = w.Write([]byte(html))
}

