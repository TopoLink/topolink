package semantic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIEnricher(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"kind\":\"middleware-purpose\",\"text\":\"JWT auth guard\",\"effects\":[{\"condition\":\"no token\",\"action\":\"abort 401\",\"business\":\"rejects anonymous\"},{\"condition\":\"tenant mismatch\",\"action\":\"abort 403\",\"business\":\"prevents cross-tenant access\"}],\"confidence\":0.9}"}}]}`))
	}))
	defer srv.Close()

	e := &OpenAIEnricher{BaseURL: srv.URL, APIKey: "test-key", Model: "m1", HTTP: srv.Client()}
	ann, err := e.Annotate(context.Background(), Unit{
		ID:     "middleware:Auth",
		Kind:   "middleware",
		Source: "func Auth() error { return nil }",
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth = %q, want Bearer test-key", gotAuth)
	}
	if ann.Text == "" || ann.Kind != "middleware-purpose" || ann.Confidence != 0.9 {
		t.Errorf("annotation = %+v", ann)
	}
	if len(ann.Effects) != 2 {
		t.Fatalf("effects = %d, want 2: %+v", len(ann.Effects), ann.Effects)
	}
	if ann.Effects[1].Business != "prevents cross-tenant access" {
		t.Errorf("effects[1].business = %q", ann.Effects[1].Business)
	}

	bodyBytes, _ := json.Marshal(gotBody)
	if !strings.Contains(string(bodyBytes), "func Auth") {
		t.Errorf("request body should include unit source; got %s", bodyBytes)
	}
}

func TestParseAnnotationFenced(t *testing.T) {
	ann, err := parseAnnotationContent("```json\n{\"kind\":\"business-impact\",\"text\":\"x\",\"confidence\":0.5}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if ann.Text != "x" || ann.Confidence != 0.5 {
		t.Errorf("got %+v", ann)
	}
}
