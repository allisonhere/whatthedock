package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withFakeUpstream points httpClient's transport at server's URL, rewriting
// any outbound host to it — the same rewriteHostTransport pattern used by
// internal/update's tests, since Analyze's endpoints are hardcoded (not
// injectable via a base-URL parameter) for every provider except custom.
func withFakeUpstream(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := httpClient
	httpClient = &http.Client{Transport: rewriteHostTransport{target: server.URL, base: http.DefaultTransport}}
	t.Cleanup(func() { httpClient = original })
}

type rewriteHostTransport struct {
	target string
	base   http.RoundTripper
}

func (rt rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	targetURL, err := req.URL.Parse(rt.target)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.Host = targetURL.Host
	return rt.base.RoundTrip(req)
}

func TestAnalyzeAnthropicSendsRequestAndParsesResponse(t *testing.T) {
	var gotPath, gotAPIKey, gotVersion string
	var gotBody anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"restart it"}]}`))
	}))
	defer server.Close()
	withFakeUpstream(t, server)

	got, err := Analyze(context.Background(), Config{Provider: ProviderAnthropic, APIKey: "sk-ant-test"}, "why is this unhealthy?")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got != "restart it" {
		t.Fatalf("Analyze() = %q, want %q", got, "restart it")
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
	if gotAPIKey != "sk-ant-test" {
		t.Fatalf("x-api-key = %q, want sk-ant-test", gotAPIKey)
	}
	if gotVersion != anthropicVersion {
		t.Fatalf("anthropic-version = %q, want %q", gotVersion, anthropicVersion)
	}
	if gotBody.Model != anthropicDefaultModel {
		t.Fatalf("model = %q, want default %q", gotBody.Model, anthropicDefaultModel)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Content != "why is this unhealthy?" {
		t.Fatalf("messages = %#v, want the prompt as the single user message", gotBody.Messages)
	}
}

func TestAnalyzeAnthropicUsesConfiguredModel(t *testing.T) {
	var gotBody anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()
	withFakeUpstream(t, server)

	_, err := Analyze(context.Background(), Config{Provider: ProviderAnthropic, APIKey: "k", Model: "claude-3-5-sonnet-latest"}, "p")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if gotBody.Model != "claude-3-5-sonnet-latest" {
		t.Fatalf("model = %q, want the configured override", gotBody.Model)
	}
}

func TestAnalyzeAnthropicMissingAPIKey(t *testing.T) {
	_, err := Analyze(context.Background(), Config{Provider: ProviderAnthropic}, "p")
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("error = %v, want a clear no-API-key error", err)
	}
}

func TestAnalyzeAnthropicWrapsProviderErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error":{"message":"invalid x-api-key"}}`))
	}))
	defer server.Close()
	withFakeUpstream(t, server)

	_, err := Analyze(context.Background(), Config{Provider: ProviderAnthropic, APIKey: "bad"}, "p")
	if err == nil || !strings.Contains(err.Error(), "invalid x-api-key") || !strings.Contains(err.Error(), "anthropic:") {
		t.Fatalf("error = %v, want it to name the provider and include the API's own message", err)
	}
}

func TestAnalyzeAnthropicNon2xxSurfacesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"authentication_error"}}`))
	}))
	defer server.Close()
	withFakeUpstream(t, server)

	_, err := Analyze(context.Background(), Config{Provider: ProviderAnthropic, APIKey: "bad"}, "p")
	if err == nil || !strings.Contains(err.Error(), "authentication_error") {
		t.Fatalf("error = %v, want the non-2xx body's message surfaced", err)
	}
}

func TestAnalyzeOpenAISendsRequestAndParsesResponse(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"check logs"}}]}`))
	}))
	defer server.Close()
	withFakeUpstream(t, server)

	got, err := Analyze(context.Background(), Config{Provider: ProviderOpenAI, APIKey: "sk-test"}, "why crash-looping?")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got != "check logs" {
		t.Fatalf("Analyze() = %q, want %q", got, "check logs")
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want Bearer sk-test", gotAuth)
	}
	if gotBody.Model != openAIDefaultModel {
		t.Fatalf("model = %q, want default %q", gotBody.Model, openAIDefaultModel)
	}
}

func TestAnalyzeOpenAIMissingAPIKey(t *testing.T) {
	_, err := Analyze(context.Background(), Config{Provider: ProviderOpenAI}, "p")
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("error = %v, want a clear no-API-key error", err)
	}
}

func TestAnalyzeGeminiSendsRequestAndParsesResponse(t *testing.T) {
	var gotPath, gotQuery string
	var gotBody geminiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"looks fine"}]}}]}`))
	}))
	defer server.Close()
	withFakeUpstream(t, server)

	got, err := Analyze(context.Background(), Config{Provider: ProviderGemini, APIKey: "gm-test"}, "diagnose this")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got != "looks fine" {
		t.Fatalf("Analyze() = %q, want %q", got, "looks fine")
	}
	if gotPath != "/v1beta/models/"+geminiDefaultModel+":generateContent" {
		t.Fatalf("path = %q, want the default model's generateContent path", gotPath)
	}
	if gotQuery != "gm-test" {
		t.Fatalf("key query param = %q, want gm-test", gotQuery)
	}
	if len(gotBody.Contents) != 1 || len(gotBody.Contents[0].Parts) != 1 || gotBody.Contents[0].Parts[0].Text != "diagnose this" {
		t.Fatalf("contents = %#v, want the prompt as the single part", gotBody.Contents)
	}
}

func TestAnalyzeGeminiMissingAPIKey(t *testing.T) {
	_, err := Analyze(context.Background(), Config{Provider: ProviderGemini}, "p")
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("error = %v, want a clear no-API-key error", err)
	}
}

func TestAnalyzeCustomWorksWithoutAPIKey(t *testing.T) {
	var gotAuth string
	var sawAuthHeader bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, sawAuthHeader = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"local model reply"}}]}`))
	}))
	defer server.Close()

	got, err := Analyze(context.Background(), Config{Provider: ProviderCustom, BaseURL: server.URL}, "p")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got != "local model reply" {
		t.Fatalf("Analyze() = %q, want %q", got, "local model reply")
	}
	if sawAuthHeader {
		t.Fatalf("Authorization header = %q, want none sent when no API key is configured", gotAuth)
	}
}

func TestAnalyzeCustomSendsAPIKeyWhenConfigured(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	_, err := Analyze(context.Background(), Config{Provider: ProviderCustom, BaseURL: server.URL, APIKey: "local-key"}, "p")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if gotAuth != "Bearer local-key" {
		t.Fatalf("Authorization = %q, want Bearer local-key", gotAuth)
	}
}

func TestAnalyzeCustomMissingBaseURL(t *testing.T) {
	_, err := Analyze(context.Background(), Config{Provider: ProviderCustom}, "p")
	if err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("error = %v, want a clear no-base-URL error", err)
	}
}

func TestAnalyzeUnknownProvider(t *testing.T) {
	_, err := Analyze(context.Background(), Config{Provider: "made-up"}, "p")
	if err == nil || !strings.Contains(err.Error(), "made-up") {
		t.Fatalf("error = %v, want it to name the unrecognized provider", err)
	}
}
