package provider_ollama

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

type capturedShowRequest struct {
	Model string `json:"model"`
}

func TestDiscoverer_DiscoverModelsFetchesTagsAndShowMetadata(t *testing.T) {
	var showRequests []capturedShowRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{
				"models":[
					{"name":"llama3.2:3b","details":{"family":"llama"}},
					{"model":"deepseek-r1:7b","details":{"family":"deepseek"}}
				]
			}`))
		case "/api/show":
			var req capturedShowRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			showRequests = append(showRequests, req)
			if req.Model == "llama3.2:3b" {
				_, _ = w.Write([]byte(`{
					"capabilities":["completion","tools","vision"],
					"model_info":{"llama.context_length":131072}
				}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"capabilities":["completion"],
				"model_info":{"context_length":32768}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	discoverer, err := NewDiscoverer(server.URL)
	require.NoError(t, err)

	models, err := discoverer.DiscoverModels(t.Context())

	require.NoError(t, err)
	require.Equal(t, []capturedShowRequest{{Model: "llama3.2:3b"}, {Model: "deepseek-r1:7b"}}, showRequests)
	require.Equal(t, []modelprovider.ModelDefinition{
		{
			ID:            "llama3.2:3b",
			Name:          "llama3.2",
			Owner:         constants.ModelProviderOllama,
			Provider:      constants.ModelProviderOllama,
			API:           modelprovider.APIOllamaNative,
			Input:         []modelprovider.InputKind{modelprovider.InputText, modelprovider.InputImage},
			SupportsTools: true,
			ContextWindow: 131072,
		},
		{
			ID:            "deepseek-r1:7b",
			Name:          "deepseek-r1",
			Owner:         constants.ModelProviderOllama,
			Provider:      constants.ModelProviderOllama,
			API:           modelprovider.APIOllamaNative,
			Input:         []modelprovider.InputKind{modelprovider.InputText},
			Reasoning:     true,
			ContextWindow: 32768,
		},
	}, models)
}

func TestDiscoverer_DiscoverModelsKeepsTagsWhenShowFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:3b"}]}`))
		case "/api/show":
			http.Error(w, "missing", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	discoverer, err := NewDiscoverer(server.URL)
	require.NoError(t, err)

	models, err := discoverer.DiscoverModels(t.Context())

	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "llama3.2:3b", models[0].ID)
	require.Equal(t, []modelprovider.InputKind{modelprovider.InputText}, models[0].Input)
	require.Zero(t, models[0].ContextWindow)
}

func TestDiscoverer_DiscoverModelsMarksEmbeddingOnlyModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text:latest"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{
				"capabilities":["embedding"],
				"model_info":{"bert.context_length":2048}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	discoverer, err := NewDiscoverer(server.URL)
	require.NoError(t, err)

	models, err := discoverer.DiscoverModels(t.Context())

	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "nomic-embed-text:latest", models[0].ID)
	require.Equal(t, modelprovider.APIOllamaEmbeddings, models[0].API)
	require.Equal(t, []modelprovider.InputKind{modelprovider.InputText}, models[0].Input)
	require.Equal(t, 2048, models[0].ContextWindow)
}

func TestDiscoverer_DiscoverModelsSkipsBlankTagModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":" "},{"name":"llama3.2:3b"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	discoverer, err := NewDiscoverer(server.URL)
	require.NoError(t, err)

	models, err := discoverer.DiscoverModels(t.Context())

	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "llama3.2:3b", models[0].ID)
}

func TestDiscoverer_DiscoverModelsAllowsReachableServerWithNoModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/tags", r.URL.Path)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	discoverer, err := NewDiscoverer(server.URL)
	require.NoError(t, err)

	models, err := discoverer.DiscoverModels(t.Context())

	require.NoError(t, err)
	require.Empty(t, models)
}

func TestDiscoverer_DiscoverModelsReturnsTagsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not running", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	discoverer, err := NewDiscoverer(server.URL)
	require.NoError(t, err)

	_, err = discoverer.DiscoverModels(t.Context())

	require.EqualError(t, err, "ollama request failed with status 503: not running")
}

func TestDiscoverer_DiscoverModelsReturnsNetworkError(t *testing.T) {
	discoverer, err := newDiscoverer("http://127.0.0.1:11434", roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		},
	))
	require.NoError(t, err)

	_, err = discoverer.DiscoverModels(t.Context())

	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:11434")
	require.ErrorContains(t, err, "dial failed")
}

func TestDiscoverer_DiscoverModelsReturnsReadError(t *testing.T) {
	discoverer, err := newDiscoverer("http://127.0.0.1:11434", roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errorReader{},
			}, nil
		},
	))
	require.NoError(t, err)

	_, err = discoverer.DiscoverModels(t.Context())

	require.ErrorContains(t, err, "decode Ollama response")
	require.ErrorContains(t, err, "read failed")
}

func TestDiscoverer_FetchTagsReturnsRequestBuildError(t *testing.T) {
	discoverer := &Discoverer{baseURL: "%", httpClient: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent when URL construction fails")
			return nil, nil
		},
	)}

	_, err := discoverer.fetchTags(t.Context())

	require.ErrorContains(t, err, "invalid URL")
}

func TestDiscoverer_FetchShowReturnsRequestBuildError(t *testing.T) {
	discoverer := &Discoverer{baseURL: "%", httpClient: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent when URL construction fails")
			return nil, nil
		},
	)}

	_, err := discoverer.fetchShow(t.Context(), "llama3.2:3b")

	require.ErrorContains(t, err, "invalid URL")
}

func TestDiscoverer_FetchShowReturnsConnectionError(t *testing.T) {
	discoverer, err := newDiscoverer("http://127.0.0.1:11434", roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		},
	))
	require.NoError(t, err)

	_, err = discoverer.fetchShow(t.Context(), "llama3.2:3b")

	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:11434")
}

func TestDiscoverer_DiscoverModelsReturnsDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{bad json}"))
	}))
	defer server.Close()

	discoverer, err := NewDiscoverer(server.URL)
	require.NoError(t, err)

	_, err = discoverer.DiscoverModels(t.Context())

	require.ErrorContains(t, err, "decode Ollama response")
}

func TestDiscoverer_RejectsInvalidConstruction(t *testing.T) {
	_, err := NewDiscoverer(" ")
	require.EqualError(t, err, "ollama base URL is required")

	_, err = newDiscoverer("http://127.0.0.1:11434", nil)
	require.EqualError(t, err, "ollama HTTP client is required")

	var discoverer *Discoverer
	_, err = discoverer.DiscoverModels(t.Context())
	require.EqualError(t, err, "ollama discoverer is required")
}

func TestDiscoveryHelpers(t *testing.T) {
	require.Equal(t, "from-model", getTagModelID(tagModel{Name: "from-name", Model: "from-model"}))
	require.Equal(t, "from-name", getTagModelID(tagModel{Name: "from-name"}))
	require.Empty(t, getTagModelID(tagModel{}))
	require.Empty(t, getOllamaModelDisplayName(" "))
	require.False(t, hasOllamaCapability(showResponse{}, " "))
	require.Equal(t, 4096, numberToInt(json.Number("4096")))
	require.Equal(t, 2048, numberToInt(2048))
	require.Zero(t, numberToInt("4096"))
	require.Zero(t, getOllamaContextWindow(showResponse{ModelInfo: map[string]any{"other": 4096}}))
	require.False(t, isOllamaReasoningModel("llama3.2:3b"))
}
