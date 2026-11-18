// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package webapi

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/Sakura520222/Sakura-Bot/internal/version"
)

// Snapshot of the FastAPI contract at the migration baseline. Go serves it
// without a Python dependency, and the route test checks it against the mux.
//
//go:embed openapi.json
var legacyOpenAPI []byte

var currentOpenAPI = sync.OnceValues(func() ([]byte, error) {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(legacyOpenAPI, &schema); err != nil {
		return nil, err
	}
	var info map[string]json.RawMessage
	if err := json.Unmarshal(schema["info"], &info); err != nil {
		return nil, err
	}
	release, err := json.Marshal(version.Version)
	if err != nil {
		return nil, err
	}
	info["version"] = release
	schema["info"], err = json.Marshal(info)
	if err != nil {
		return nil, err
	}
	return json.Marshal(schema)
})

func (s *Server) registerDocsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /openapi.json", s.serveOpenAPI)
	mux.HandleFunc("GET /api/docs", docsPage(swaggerPage))
	mux.HandleFunc("GET /api/redoc", docsPage(redocPage))
	mux.HandleFunc("GET /docs/oauth2-redirect", docsPage(swaggerRedirectPage))
}

func (s *Server) serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	doc, err := currentOpenAPI()
	if err != nil {
		s.logger.Error("读取 OpenAPI 描述失败", "error", err)
		writeHTTPError(w, http.StatusInternalServerError, "OpenAPI 描述不可用")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodGet {
		_, _ = w.Write(doc)
	}
}

func docsPage(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(page))
		}
	}
}

const swaggerPage = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sakura-Bot WebUI API · Swagger UI</title>
  <link rel="icon" href="/favicon.svg">
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: "/openapi.json",
      dom_id: "#swagger-ui",
      layout: "BaseLayout",
      deepLinking: true,
      showExtensions: true,
      showCommonExtensions: true,
      oauth2RedirectUrl: location.origin + "/docs/oauth2-redirect",
      presets: [SwaggerUIBundle.presets.apis]
    });
  </script>
</body>
</html>`

const redocPage = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sakura-Bot WebUI API · ReDoc</title>
  <link rel="icon" href="/favicon.svg">
  <style>body { margin: 0; }</style>
</head>
<body>
  <redoc spec-url="/openapi.json"></redoc>
  <script src="https://cdn.jsdelivr.net/npm/redoc@2/bundles/redoc.standalone.js"></script>
</body>
</html>`

const swaggerRedirectPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Swagger UI OAuth2 redirect</title>
</head>
<body>
  <script>
    const flow = window.opener?.swaggerUIRedirectOauth2;
    if (flow) {
      const values = Object.fromEntries(new URLSearchParams((location.hash || location.search).slice(1)));
      const isValid = values.state === flow.state;
      if (!isValid) {
        flow.errCb({authId: flow.auth.name, source: "auth", level: "warning", message: "OAuth state mismatch"});
      } else if (values.code) {
        flow.auth.code = values.code;
        flow.callback({auth: flow.auth, redirectUrl: flow.redirectUrl});
      } else {
        flow.callback({auth: flow.auth, token: values, isValid, redirectUrl: flow.redirectUrl});
      }
    }
    window.close();
  </script>
</body>
</html>`
