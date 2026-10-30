package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/topolink/topolink/pkg/ast"
)

// OpenAIEnricher 通过 OpenAI 兼容的 /chat/completions 接口生成语义标注。
// 兼容 OpenAI、Qwen(DashScope 兼容模式)、本地 vLLM / Ollama 等。
type OpenAIEnricher struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// NewOpenAIEnricher 创建 enricher（baseURL/model 为空时使用默认值）。
func NewOpenAIEnricher(baseURL, apiKey, model string) *OpenAIEnricher {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &OpenAIEnricher{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

const annotationSystemPrompt = `You annotate one code unit for a code-intelligence graph. Capture BOTH its purpose and HOW it affects behavior/business.

Given a unit kind (entry / middleware / table / rpc) and its source:
- middleware: enumerate EACH conditional branch (if / switch / early return) that can abort or change the request. For every branch output: condition, action (e.g. "abort 401", "abort 403", "continue"), and the business consequence (e.g. "blocks cross-tenant access"). Do NOT collapse multiple branches into one.
- entry: describe the business capability and the data/resources touched. Effects may cover success vs error paths.
- table: what business data it holds and how sensitive it is.
- rpc: the contract and expected implementation semantics.

Hard rules:
- Base everything ONLY on the given source. Do not invent conditions, branches, tables, or services.
- If the source is insufficient, say "insufficient evidence" and use a low confidence.
- Respond with a single JSON object, no markdown:
  {"kind":"middleware-purpose|business-impact|data-sensitivity|rpc-contract","text":"...","effects":[{"condition":"...","action":"...","business":"..."}],"confidence":0.0-1.0}`

func buildAnnotationUserPrompt(u Unit) string {
	return fmt.Sprintf("unit_kind: %s\nunit_id: %s\nsource:\n%s", u.Kind, u.ID, u.Source)
}

// Annotate 调用 LLM 生成标注。
func (e *OpenAIEnricher) Annotate(ctx context.Context, u Unit) (Annotation, error) {
	reqBody := map[string]any{
		"model":       e.Model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": annotationSystemPrompt},
			{"role": "user", "content": buildAnnotationUserPrompt(u)},
		},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return Annotation{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return Annotation{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}

	resp, err := e.HTTP.Do(req)
	if err != nil {
		return Annotation{}, fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Annotation{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var cr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		return Annotation{}, fmt.Errorf("decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return Annotation{}, fmt.Errorf("llm returned no choices")
	}

	ann, err := parseAnnotationContent(cr.Choices[0].Message.Content)
	if err != nil {
		return Annotation{}, err
	}
	ann.Model = e.Model
	if len(ann.Evidence) == 0 && u.Loc.File != "" {
		ann.Evidence = []ast.Loc{u.Loc}
	}
	return ann, nil
}

func parseAnnotationContent(content string) (Annotation, error) {
	s := strings.TrimSpace(content)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return Annotation{}, fmt.Errorf("no JSON object in LLM response")
	}

	var raw struct {
		Kind       string   `json:"kind"`
		Text       string   `json:"text"`
		Effects    []Effect `json:"effects"`
		Confidence float64  `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &raw); err != nil {
		return Annotation{}, fmt.Errorf("parse annotation JSON: %w", err)
	}
	if strings.TrimSpace(raw.Text) == "" {
		return Annotation{}, fmt.Errorf("empty annotation text")
	}
	return Annotation{Kind: raw.Kind, Text: raw.Text, Effects: raw.Effects, Confidence: raw.Confidence}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
