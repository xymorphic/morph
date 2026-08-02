package provider_ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

type Puller struct {
	baseURL    string
	headers    map[string]string
	httpClient httpDoer
}

type PullProgress struct {
	Status    string
	Total     int64
	Completed int64
}

type pullChunk struct {
	Status    string `json:"status"`
	Total     int64  `json:"total"`
	Completed int64  `json:"completed"`
	Error     string `json:"error"`
}

func NewPuller(baseURL string, headers map[string]string) (*Puller, error) {
	return newPuller(baseURL, headers, http.DefaultClient)
}

func newPuller(baseURL string, headers map[string]string, httpClient httpDoer) (*Puller, error) {
	normalizedBaseURL, err := normalizePullBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, errors.New("ollama HTTP client is required")
	}

	return &Puller{
		baseURL:    normalizedBaseURL,
		headers:    normalizeHeaders(headers),
		httpClient: httpClient,
	}, nil
}

func EnsureModel(
	ctx context.Context,
	baseURL string,
	model string,
	headers map[string]string,
	onProgress func(PullProgress),
) error {
	puller, err := NewPuller(baseURL, headers)
	if err != nil {
		return err
	}

	return puller.EnsureModel(ctx, model, onProgress)
}

func (p *Puller) EnsureModel(ctx context.Context, model string, onProgress func(PullProgress)) error {
	if p == nil {
		return errors.New("ollama puller is required")
	}

	model = NormalizeModelID(model)
	if model == "" {
		return errors.New("ollama model is required")
	}
	if isOllamaCloudModel(model) {
		return nil
	}

	hasModel, err := p.HasModel(ctx, model)
	if err != nil {
		return err
	}
	if hasModel {
		return nil
	}

	return p.PullModel(ctx, model, onProgress)
}

func (p *Puller) HasModel(ctx context.Context, model string) (bool, error) {
	if p == nil {
		return false, errors.New("ollama puller is required")
	}

	model = NormalizeModelID(model)
	if model == "" {
		return false, errors.New("ollama model is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return false, err
	}
	p.setHeaders(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false, enrichOllamaConnectionError(p.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tags tagsResponse
	if err := decodeOllamaResponse(resp, &tags); err != nil {
		return false, err
	}

	for _, tag := range tags.Models {
		if getTagModelID(tag) == model {
			return true, nil
		}
	}

	return false, nil
}

func (p *Puller) PullModel(ctx context.Context, model string, onProgress func(PullProgress)) error {
	if p == nil {
		return errors.New("ollama puller is required")
	}

	model = NormalizeModelID(model)
	if model == "" {
		return errors.New("ollama model is required")
	}

	body := strings.NewReader(`{"name":` + strconv.Quote(model) + `}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/pull", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	p.setHeaders(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return enrichOllamaConnectionError(p.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ollamaStatusErrorForModel(resp, model)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var chunk pullChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			return fmt.Errorf("decode Ollama pull chunk: %w", err)
		}
		errorValue := str.String(chunk.Error)
		if errorValue.Trim() != "" {
			return enrichOllamaPullError(chunk.Error, model)
		}
		if onProgress != nil {
			statusValue := str.String(chunk.Status)
			onProgress(PullProgress{
				Status:    statusValue.Trim(),
				Total:     chunk.Total,
				Completed: chunk.Completed,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func (p *Puller) setHeaders(req *http.Request) {
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
}

func isOllamaCloudModel(model string) bool {
	modelValue := str.String(model)
	return strings.HasSuffix(modelValue.Normalized(), ":cloud")
}

func normalizePullBaseURL(value string) (string, error) {
	valueText := strings.TrimRight(str.String(value).Trim(), "/")
	if valueText == "" {
		return "", errors.New("ollama base URL is required")
	}

	parsed, err := url.Parse(valueText)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("ollama base URL %q is invalid", valueText)
	}
	if strings.EqualFold(strings.TrimRight(parsed.Path, "/"), "/v1") {
		parsed.Path = ""
		parsed.RawPath = ""
		valueText = strings.TrimRight(parsed.String(), "/")
	}

	return normalizeBaseURL(valueText)
}
