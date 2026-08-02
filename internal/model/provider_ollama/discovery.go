package provider_ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/pkg/str"
)

type Discoverer struct {
	baseURL    string
	httpClient httpDoer
}

type tagsResponse struct {
	Models []tagModel `json:"models"`
}

type tagModel struct {
	Name    string       `json:"name"`
	Model   string       `json:"model"`
	Details modelDetails `json:"details"`
}

type modelDetails struct {
	Family string `json:"family"`
}

type showResponse struct {
	ModelInfo    map[string]any `json:"model_info"`
	Capabilities []string       `json:"capabilities"`
}

func NewDiscoverer(baseURL string) (*Discoverer, error) {
	return newDiscoverer(baseURL, http.DefaultClient)
}

func newDiscoverer(baseURL string, httpClient httpDoer) (*Discoverer, error) {
	normalizedBaseURL, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, fmt.Errorf("ollama HTTP client is required")
	}

	return &Discoverer{baseURL: normalizedBaseURL, httpClient: httpClient}, nil
}

func (d *Discoverer) DiscoverModels(ctx context.Context) ([]modelprovider.ModelDefinition, error) {
	if d == nil {
		return nil, fmt.Errorf("ollama discoverer is required")
	}

	tags, err := d.fetchTags(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]modelprovider.ModelDefinition, 0, len(tags.Models))
	for _, tag := range tags.Models {
		modelID := getTagModelID(tag)
		if modelID == "" {
			continue
		}

		show, _ := d.fetchShow(ctx, modelID)
		models = append(models, modelDefinitionFromOllamaModel(tag, show))
	}

	return models, nil
}

func (d *Discoverer) fetchTags(ctx context.Context) (tagsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/api/tags", nil)
	if err != nil {
		return tagsResponse{}, err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return tagsResponse{}, enrichOllamaConnectionError(d.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tags tagsResponse
	if err := decodeOllamaResponse(resp, &tags); err != nil {
		return tagsResponse{}, err
	}

	return tags, nil
}

func (d *Discoverer) fetchShow(ctx context.Context, model string) (showResponse, error) {
	body := strings.NewReader(`{"model":` + strconv.Quote(model) + `}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/api/show", body)
	if err != nil {
		return showResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return showResponse{}, enrichOllamaConnectionError(d.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var show showResponse
	if err := decodeOllamaResponse(resp, &show); err != nil {
		return showResponse{}, err
	}

	return show, nil
}

func modelDefinitionFromOllamaModel(tag tagModel, show showResponse) modelprovider.ModelDefinition {
	modelID := getTagModelID(tag)
	return modelprovider.ModelDefinition{
		ID:            modelID,
		Name:          getOllamaModelDisplayName(modelID),
		Owner:         constants.ModelProviderOllama,
		Provider:      constants.ModelProviderOllama,
		API:           getOllamaModelAPI(show),
		Input:         getOllamaModelInputs(show),
		Reasoning:     isOllamaReasoningModel(modelID),
		SupportsTools: hasOllamaCapability(show, "tools"),
		ContextWindow: getOllamaContextWindow(show),
	}
}

func getOllamaModelAPI(show showResponse) string {
	if hasOllamaCapability(show, "embedding") && !hasOllamaCapability(show, "completion") {
		return modelprovider.APIOllamaEmbeddings
	}

	return modelprovider.APIOllamaNative
}

func getTagModelID(tag tagModel) string {
	modelValue := str.String(tag.Model)
	if model := modelValue.Trim(); model != "" {
		return model
	}
	nameValue := str.String(tag.Name)
	return nameValue.Trim()
}

func getOllamaModelDisplayName(modelID string) string {
	modelIDValue := str.String(modelID)
	modelID = modelIDValue.Trim()
	if modelID == "" {
		return ""
	}

	name, _, _ := strings.Cut(modelID, ":")
	nameValue2 := str.String(name)
	return nameValue2.Trim()
}

func getOllamaModelInputs(show showResponse) []modelprovider.InputKind {
	inputs := []modelprovider.InputKind{modelprovider.InputText}
	if hasOllamaCapability(show, "vision") {
		inputs = append(inputs, modelprovider.InputImage)
	}

	return inputs
}

func getOllamaContextWindow(show showResponse) int {
	for key, value := range show.ModelInfo {
		keyValue := str.String(key)
		key = keyValue.Normalized()
		if key != "context_length" && !strings.HasSuffix(key, ".context_length") {
			continue
		}
		if contextWindow := numberToInt(value); contextWindow > 0 {
			return contextWindow
		}
	}

	return 0
}

func numberToInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		converted, _ := typed.Int64()
		return int(converted)
	default:
		return 0
	}
}

func hasOllamaCapability(show showResponse, capability string) bool {
	capabilityValue := str.String(capability)
	capability = capabilityValue.Normalized()
	if capability == "" {
		return false
	}

	for _, value := range show.Capabilities {
		valueText := str.String(value)
		if valueText.Normalized() == capability {
			return true
		}
	}

	return false
}

func isOllamaReasoningModel(modelID string) bool {
	modelIDValue2 := str.String(modelID)
	modelID = modelIDValue2.Normalized()
	for _, marker := range []string{
		"deepseek-r1",
		"qwen3",
		"qwq",
		"thinking",
	} {
		if strings.Contains(modelID, marker) {
			return true
		}
	}

	return false
}
