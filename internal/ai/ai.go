// Package ai is a thin, dependency-free HTTP client for asking a
// third-party AI provider to analyze a prompt whatthedock builds elsewhere
// (see the Problems pane's "analyze with AI" action in internal/ui). It
// mirrors internal/update's shape — a package-level http.Client, a
// context-first exported entry point, typed request/response structs, no
// SDK — and stays deliberately dumb: it knows nothing about Settings, env
// vars, or UI concerns. The caller resolves Config (including which API
// key to use) before calling Analyze.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider identifies which AI API Analyze should call. Custom covers any
// OpenAI-compatible endpoint (a local Ollama server, OpenRouter, Azure
// OpenAI, ...) by base URL rather than requiring a hardcoded name for each
// one — that's how this covers "the major players" without an
// ever-growing provider list.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderGemini    Provider = "gemini"
	ProviderCustom    Provider = "custom"
)

// Config is everything Analyze needs to reach a provider. APIKey/BaseURL
// are resolved by the caller — env var checked before a stored Settings
// value, see internal/ui — Analyze itself does no such resolution.
type Config struct {
	Provider Provider
	Model    string // empty uses that provider's own default
	APIKey   string
	BaseURL  string // only meaningful for ProviderCustom
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

const (
	anthropicEndpoint     = "https://api.anthropic.com/v1/messages"
	anthropicVersion      = "2023-06-01"
	anthropicDefaultModel = "claude-3-5-haiku-latest"

	openAIEndpoint     = "https://api.openai.com/v1/chat/completions"
	openAIDefaultModel = "gpt-4o-mini"

	geminiEndpoint     = "https://generativelanguage.googleapis.com/v1beta/models"
	geminiDefaultModel = "gemini-1.5-flash"
)

// Analyze sends prompt to cfg.Provider and returns its text response, or a
// wrapped error naming the provider — never a raw HTTP/JSON error, mirroring
// this repo's friendlyDockerError/friendlyComposeBaseFileError convention
// of always explaining what actually happened.
func Analyze(ctx context.Context, cfg Config, prompt string) (string, error) {
	switch cfg.Provider {
	case ProviderAnthropic:
		text, err := analyzeAnthropic(ctx, cfg, prompt)
		if err != nil {
			return "", fmt.Errorf("anthropic: %w", err)
		}
		return text, nil
	case ProviderOpenAI:
		text, err := analyzeChatCompletions(ctx, openAIEndpoint, cfg, openAIDefaultModel, true, prompt)
		if err != nil {
			return "", fmt.Errorf("openai: %w", err)
		}
		return text, nil
	case ProviderGemini:
		text, err := analyzeGemini(ctx, cfg, prompt)
		if err != nil {
			return "", fmt.Errorf("gemini: %w", err)
		}
		return text, nil
	case ProviderCustom:
		base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
		if base == "" {
			return "", errors.New("custom: no base URL configured")
		}
		text, err := analyzeChatCompletions(ctx, base+"/chat/completions", cfg, cfg.Model, false, prompt)
		if err != nil {
			return "", fmt.Errorf("custom: %w", err)
		}
		return text, nil
	default:
		return "", fmt.Errorf("unknown AI provider %q", cfg.Provider)
	}
}

// apiError is the {"error":{"message":"..."}} shape every provider here
// uses for a well-formed failure response.
type apiError struct {
	Message string `json:"message"`
}

// doJSON POSTs reqBody as JSON to endpoint with headers, decoding a JSON
// response into respBody. A non-2xx status is reported with the response
// body (trimmed) so a well-formed provider error message survives even
// though respBody's shape wasn't matched.
func doJSON(ctx context.Context, endpoint string, headers map[string]string, reqBody, respBody any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("request failed (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, respBody); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *apiError `json:"error"`
}

func analyzeAnthropic(ctx context.Context, cfg Config, prompt string) (string, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return "", errors.New("no API key configured")
	}
	model := cfg.Model
	if model == "" {
		model = anthropicDefaultModel
	}
	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 1024,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	}
	headers := map[string]string{
		"x-api-key":         cfg.APIKey,
		"anthropic-version": anthropicVersion,
	}
	var resp anthropicResponse
	if err := doJSON(ctx, anthropicEndpoint, headers, reqBody, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", errors.New(resp.Error.Message)
	}
	var text strings.Builder
	for _, part := range resp.Content {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	if text.Len() == 0 {
		return "", errors.New("empty response")
	}
	return strings.TrimSpace(text.String()), nil
}

// chatRequest/chatResponse are the OpenAI Chat Completions shape, shared by
// ProviderOpenAI and ProviderCustom — the point of "custom" is exactly this
// reuse, covering any OpenAI-compatible server without a name of its own.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *apiError `json:"error"`
}

// analyzeChatCompletions is analyzeAnthropic's counterpart for the
// OpenAI-shaped APIs. requireKey is false for ProviderCustom, since plenty
// of self-hosted OpenAI-compatible servers (a local Ollama, for instance)
// don't gate on one — when APIKey is set anyway it's still sent as a
// bearer token, since some custom endpoints do want it.
func analyzeChatCompletions(ctx context.Context, endpoint string, cfg Config, defaultModel string, requireKey bool, prompt string) (string, error) {
	if requireKey && strings.TrimSpace(cfg.APIKey) == "" {
		return "", errors.New("no API key configured")
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel
	}
	reqBody := chatRequest{Model: model, Messages: []chatMessage{{Role: "user", Content: prompt}}}
	headers := map[string]string{}
	if strings.TrimSpace(cfg.APIKey) != "" {
		headers["Authorization"] = "Bearer " + cfg.APIKey
	}
	var resp chatResponse
	if err := doJSON(ctx, endpoint, headers, reqBody, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", errors.New(resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("empty response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *apiError `json:"error"`
}

func analyzeGemini(ctx context.Context, cfg Config, prompt string) (string, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return "", errors.New("no API key configured")
	}
	model := cfg.Model
	if model == "" {
		model = geminiDefaultModel
	}
	endpoint := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiEndpoint, model, url.QueryEscape(cfg.APIKey))
	reqBody := geminiRequest{Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}}}
	var resp geminiResponse
	if err := doJSON(ctx, endpoint, nil, reqBody, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", errors.New(resp.Error.Message)
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("empty response")
	}
	var text strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		text.WriteString(part.Text)
	}
	if text.Len() == 0 {
		return "", errors.New("empty response")
	}
	return strings.TrimSpace(text.String()), nil
}

// String renders a Provider for display (Settings row values, etc.).
func (p Provider) String() string {
	return string(p)
}
