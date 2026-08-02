package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	webintegration "github.com/xymorphic/morph/internal/providers/web"
	"github.com/xymorphic/morph/internal/tools"
)

type stubProvider struct {
	search func(context.Context, string, int) ([]webintegration.SearchResult, error)
}

func (s stubProvider) Search(ctx context.Context, query string, count int) ([]webintegration.SearchResult, error) {
	return s.search(ctx, query, count)
}

func (stubProvider) Extract(context.Context, []string) ([]webintegration.ExtractResult, error) {
	return nil, errors.New("unexpected extract call")
}

func TestWebSearch_RejectsEmptyQuery(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(context.Context, string, int) ([]webintegration.SearchResult, error) {
			t.Fatal("search should not be called")
			return nil, nil
		},
	})))

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_search", Input: `{"query":"   "}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "query is required", toolErr.Message)
}

func TestWebSearch_RejectsMalformedInput(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(context.Context, string, int) ([]webintegration.SearchResult, error) {
			t.Fatal("search should not be called")
			return nil, nil
		},
	})))

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_search", Input: `{"query":`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.NotEmpty(t, toolErr.Message)
}

func TestWebSearch_AppliesDefaultCount(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(_ context.Context, query string, count int) ([]webintegration.SearchResult, error) {
			require.Equal(t, "golang", query)
			require.Equal(t, defaultCount, count)
			return []webintegration.SearchResult{{Title: "Go", URL: "https://go.dev", Snippet: "Docs", Position: 1}}, nil
		},
	})))

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_search", Input: `{"query":"golang"}`})
	require.NoError(t, err)

	var payload struct {
		Results []webintegration.SearchResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "Go", payload.Results[0].Title)
	require.Equal(t, "title: Go\nurl: https://go.dev\nsnippet: Docs", result.SemanticContent)
}

func TestProjectSemanticContent_RejectsMalformedOutput(t *testing.T) {
	require.Empty(t, projectSemanticContent(tools.Call{}, tools.Result{Output: "not-json"}))
}

func TestWebSearch_ClampsCount(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(_ context.Context, _ string, count int) ([]webintegration.SearchResult, error) {
			require.Equal(t, maxCount, count)
			return nil, nil
		},
	})))

	_, err := registry.Invoke(context.Background(), tools.Call{Name: "web_search", Input: `{"query":"golang","count":50}`})
	require.NoError(t, err)
}

func TestWebSearch_ReturnsProviderErrorsAsToolErrors(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(context.Context, string, int) ([]webintegration.SearchResult, error) {
			return nil, errors.New("provider failed")
		},
	})))

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_search", Input: `{"query":"golang"}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "tool_error", toolErr.Code)
	require.Equal(t, "provider failed", toolErr.Message)
}

func TestWebSearch_AskPresetRequiresApprovalBeforeProviderCall(t *testing.T) {
	called := false
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetAskForApproval},
	})
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(context.Context, string, int) ([]webintegration.SearchResult, error) {
			called = true
			return nil, nil
		},
	})))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "web_search", Input: `{"query":"golang"}`,
	})

	require.NoError(t, err)
	require.False(t, called)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeApprovalRequired, toolErr.Code)
}

func TestWebSearch_ApprovePresetCallsProvider(t *testing.T) {
	called := false
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetApproveForMe},
	})
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(context.Context, string, int) ([]webintegration.SearchResult, error) {
			called = true
			return nil, nil
		},
	})))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "web_search", Input: `{"query":"golang"}`,
	})

	require.NoError(t, err)
	require.True(t, called)
	require.Empty(t, result.Error)
}

func TestWebSearch_FiltersBlockedResults(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(context.Context, string, int) ([]webintegration.SearchResult, error) {
			return []webintegration.SearchResult{
				{Title: "Allowed", URL: "https://allowed.example", Position: 1},
				{Title: "Blocked", URL: "https://blocked.example", Position: 2},
			}, nil
		},
	}, Options{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})))

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_search", Input: `{"query":"golang"}`})
	require.NoError(t, err)

	var payload struct {
		Results []webintegration.SearchResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, "Allowed", payload.Results[0].Title)
}

func TestWebSearch_ReturnsErrorWhenProviderIsNil(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(nil)))

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "web_search", Input: `{"query":"golang"}`})
	require.NoError(t, err)

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "tool_error", toolErr.Code)
	require.Equal(t, "web search provider is not configured", toolErr.Message)
}

func TestWebSearch_RequiresNetworkCapability(t *testing.T) {
	registry := tools.NewDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(Definition(stubProvider{
		search: func(context.Context, string, int) ([]webintegration.SearchResult, error) {
			return nil, nil
		},
	})))

	withNetwork, err := registry.Resolve(tools.Policy{Capabilities: tools.Capabilities{Network: true}})
	require.NoError(t, err)
	require.Len(t, withNetwork, 1)
	require.Equal(t, "web_search", withNetwork[0].Name)

	withoutNetwork, err := registry.Resolve(tools.Policy{})
	require.NoError(t, err)
	require.Empty(t, withoutNetwork)
}
