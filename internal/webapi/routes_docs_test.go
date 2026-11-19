package webapi

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/version"
)

func TestWebUIOpenAPIDocsArePublic(t *testing.T) {
	server := New(Deps{Logger: webTestLogger()})
	result := webTestResponse(t, server, "GET", "/openapi.json", "", "", 200)
	var payload struct {
		Version string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != "3.1.0" || payload.Info.Version != version.Version || len(payload.Paths) < 100 {
		t.Fatalf("unexpected OpenAPI version or path count: %q %q %d", payload.Version, payload.Info.Version, len(payload.Paths))
	}
	for path, script := range map[string]string{
		"/api/docs": "SwaggerUIBundle", "/api/redoc": "<redoc", "/docs/oauth2-redirect": "swaggerUIRedirectOauth2",
	} {
		response := webTestResponse(t, server, "GET", path, "", "", 200)
		if !strings.Contains(response.Body.String(), script) ||
			!strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("unexpected rendered documentation at %s", path)
		}
	}
}

func TestLegacyOpenAPIOperationsRouteToGoHandlers(t *testing.T) {
	var schema struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(legacyOpenAPI, &schema); err != nil {
		t.Fatal(err)
	}
	mux := (&Server{}).routes()
	type operation struct{ method, path string }
	var operations []operation
	for path, methods := range schema.Paths {
		for verb := range methods {
			if slices.Contains([]string{"get", "head", "post", "put", "patch", "delete", "options", "trace"}, verb) {
				operations = append(operations, operation{strings.ToUpper(verb), path})
			}
		}
	}
	// These routes were intentionally excluded from the FastAPI OpenAPI
	// schema, and remain part of the server's compatibility contract.
	operations = append(operations,
		operation{"GET", "/api/wiki/account/callback"},
		operation{"POST", "/api/internal/wiki-research"},
		operation{"GET", "/wiki-preview/{channel}"},
		operation{"GET", "/wiki-preview/{channel}/{slug}"},
	)
	if len(operations) != 233 {
		t.Fatalf("legacy route baseline contains %d operations, want 233", len(operations))
	}
	placeholder := regexp.MustCompile(`\{[^{}]+\}`)
	for _, route := range operations {
		sample := placeholder.ReplaceAllString(route.path, "example")
		_, pattern := mux.Handler(httptest.NewRequest(route.method, sample, nil))
		if pattern == "" || pattern == "/" || !strings.Contains(pattern, " ") {
			t.Errorf("%s %s is missing a dedicated Go handler (matches %q)", route.method, route.path, pattern)
		}
	}
}
