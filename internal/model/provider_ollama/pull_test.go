package provider_ollama

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type capturedPullRequest struct {
	Name string `json:"name"`
}

func TestPuller_EnsureModelPullsMissingModel(t *testing.T) {
	var pulled capturedPullRequest
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.Equal(t, "trace", r.Header.Get("X-Test"))

		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:3b"}]}`))
		case "/api/pull":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&pulled))
			_, _ = w.Write([]byte(`{"status":"pulling manifest"}` + "\n" + `{"status":"success"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	puller, err := NewPuller(server.URL, map[string]string{" X-Test ": " trace "})
	require.NoError(t, err)

	var progress []PullProgress
	err = puller.EnsureModel(t.Context(), "ollama/qwen3:8b", func(value PullProgress) {
		progress = append(progress, value)
	})

	require.NoError(t, err)
	require.Equal(t, []string{"/api/tags", "/api/pull"}, paths)
	require.Equal(t, capturedPullRequest{Name: "qwen3:8b"}, pulled)
	require.Equal(t, []PullProgress{
		{Status: "pulling manifest"},
		{Status: "success"},
	}, progress)
}

func TestEnsureModel_UsesDefaultPuller(t *testing.T) {
	var pulled capturedPullRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/pull":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&pulled))
			_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := EnsureModel(t.Context(), server.URL, "llama3.2:3b", nil, nil)

	require.NoError(t, err)
	require.Equal(t, capturedPullRequest{Name: "llama3.2:3b"}, pulled)
}

func TestEnsureModel_ReturnsPullerConstructionError(t *testing.T) {
	err := EnsureModel(t.Context(), "://bad", "llama3.2:3b", nil, nil)

	require.EqualError(t, err, `ollama base URL "://bad" is invalid`)
}

func TestPuller_EnsureModelSkipsAvailableAndCloudModels(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.Equal(t, "/api/tags", r.URL.Path)
		_, _ = w.Write([]byte(`{"models":[{"model":"llama3.2:3b"}]}`))
	}))
	defer server.Close()

	puller, err := NewPuller(server.URL, nil)
	require.NoError(t, err)

	require.NoError(t, puller.EnsureModel(t.Context(), "llama3.2:3b", nil))
	require.NoError(t, puller.EnsureModel(t.Context(), "kimi-k2.5:cloud", nil))

	require.Equal(t, []string{"/api/tags"}, paths)
}

func TestPuller_PullModelReturnsChunkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/pull", r.URL.Path)
		_, _ = w.Write([]byte(`{"error":"model does not exist"}` + "\n"))
	}))
	defer server.Close()

	puller, err := NewPuller(server.URL, nil)
	require.NoError(t, err)

	err = puller.PullModel(t.Context(), "missing", nil)

	require.EqualError(
		t,
		err,
		`ollama model "missing" is not installed or could not be found; run morph provider configure --provider ollama --model missing --pull or ollama pull missing: ollama pull failed: model does not exist`,
	)
}

func TestPuller_PullModelSkipsBlankChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/pull", r.URL.Path)
		_, _ = w.Write([]byte("\n" + `{"status":"downloading","completed":25,"total":100}` + "\n"))
	}))
	defer server.Close()

	puller, err := NewPuller(server.URL, nil)
	require.NoError(t, err)

	var progress []PullProgress
	err = puller.PullModel(t.Context(), "llama3.2:3b", func(value PullProgress) {
		progress = append(progress, value)
	})

	require.NoError(t, err)
	require.Equal(t, []PullProgress{{
		Status:    "downloading",
		Completed: 25,
		Total:     100,
	}}, progress)
}

func TestPuller_ReturnsProviderStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			http.Error(w, "tags failed", http.StatusServiceUnavailable)
		case "/api/pull":
			http.Error(w, "pull failed", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	puller, err := NewPuller(server.URL, nil)
	require.NoError(t, err)

	_, err = puller.HasModel(t.Context(), "llama3.2:3b")
	require.EqualError(t, err, "ollama request failed with status 503: tags failed")

	err = puller.PullModel(t.Context(), "llama3.2:3b", nil)
	require.EqualError(
		t,
		err,
		`ollama model "llama3.2:3b" is not installed or could not be found; run morph provider configure --provider ollama --model llama3.2:3b --pull or ollama pull llama3.2:3b: ollama request failed with status 404: pull failed`,
	)
}

func TestPuller_PullModelReturnsDecodeAndReadErrors(t *testing.T) {
	puller, err := newPuller("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("not-json")),
			}, nil
		},
	))
	require.NoError(t, err)

	err = puller.PullModel(t.Context(), "llama3.2:3b", nil)
	require.ErrorContains(t, err, "decode Ollama pull chunk")

	puller, err = newPuller("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errorReader{},
			}, nil
		},
	))
	require.NoError(t, err)

	err = puller.PullModel(t.Context(), "llama3.2:3b", nil)
	require.EqualError(t, err, "read failed")
}

func TestPuller_NormalizesOpenAICompatibleBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/tags", r.URL.Path)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	puller, err := NewPuller(server.URL+"/v1", nil)
	require.NoError(t, err)

	hasModel, err := puller.HasModel(t.Context(), "missing")

	require.NoError(t, err)
	require.False(t, hasModel)
}

func TestPuller_ReturnsValidationAndNetworkErrors(t *testing.T) {
	_, err := NewPuller(" ", nil)
	require.EqualError(t, err, "ollama base URL is required")

	_, err = NewPuller("://bad", nil)
	require.EqualError(t, err, `ollama base URL "://bad" is invalid`)

	_, err = newPuller("http://127.0.0.1:11434", nil, nil)
	require.EqualError(t, err, "ollama HTTP client is required")

	var puller *Puller
	err = puller.EnsureModel(t.Context(), "llama3.2:3b", nil)
	require.EqualError(t, err, "ollama puller is required")

	_, err = puller.HasModel(t.Context(), "llama3.2:3b")
	require.EqualError(t, err, "ollama puller is required")

	err = puller.PullModel(t.Context(), "llama3.2:3b", nil)
	require.EqualError(t, err, "ollama puller is required")

	puller, err = newPuller("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		},
	))
	require.NoError(t, err)

	_, err = puller.HasModel(t.Context(), "llama3.2:3b")
	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:11434")

	err = puller.EnsureModel(t.Context(), "llama3.2:3b", nil)
	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:11434")

	err = puller.PullModel(t.Context(), "llama3.2:3b", nil)
	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:11434")

	_, err = puller.HasModel(t.Context(), " ")
	require.EqualError(t, err, "ollama model is required")

	err = puller.EnsureModel(t.Context(), " ", nil)
	require.EqualError(t, err, "ollama model is required")

	err = puller.PullModel(t.Context(), " ", nil)
	require.EqualError(t, err, "ollama model is required")
}

func TestPuller_ReturnsRequestBuildError(t *testing.T) {
	puller := &Puller{
		baseURL: "%",
		httpClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent when URL construction fails")
			return nil, nil
		}),
	}

	_, err := puller.HasModel(t.Context(), "llama3.2:3b")
	require.ErrorContains(t, err, "invalid URL")

	err = puller.PullModel(t.Context(), "llama3.2:3b", nil)
	require.ErrorContains(t, err, "invalid URL")
}
