package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
)

func TestHTTPClient_PostMessageSendsSlackRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write([]byte(`{"ok":true,"ts":"123.4"}`))
	}))
	defer server.Close()
	client := NewHTTPClient("xoxb-token")
	client.baseURL = server.URL

	ts, err := client.PostMessage(context.Background(), pkgslack.Target{ChannelID: "C1", ThreadTS: "100.1"}, "hello")

	require.NoError(t, err)
	require.Equal(t, "123.4", ts)
	require.Equal(t, "/chat.postMessage", gotPath)
	require.Equal(t, "Bearer xoxb-token", gotAuth)
	require.Equal(t, map[string]any{"channel": "C1", "thread_ts": "100.1", "text": "hello"}, gotBody)
}

func TestHTTPClient_StartAppendAndStopStream(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		bodies = append(bodies, body)
		switch r.URL.Path {
		case "/chat.startStream":
			_, _ = w.Write([]byte(`{"ok":true,"channel":"C2","ts":"123.4"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	client := NewHTTPClient("xoxb-token")
	client.baseURL = server.URL
	target := pkgslack.Target{ChannelID: "C1", ThreadTS: "100.1", RecipientUserID: "U1", RecipientTeamID: "T1"}

	stream, err := client.StartStream(context.Background(), target, "start")
	require.NoError(t, err)
	require.Equal(t, pkgslack.Stream{ChannelID: "C2", TS: "123.4"}, stream)
	require.NoError(t, client.AppendStream(context.Background(), stream, []pkgslack.Chunk{
		pkgslack.MarkdownTextChunk("delta"),
	}))
	require.NoError(t, client.StopStream(context.Background(), stream, "final"))
	require.Equal(t, []string{"/chat.startStream", "/chat.appendStream", "/chat.stopStream"}, paths)
	require.Equal(t, []map[string]any{
		{
			"channel":   "C1",
			"thread_ts": "100.1",
			"chunks": []any{
				map[string]any{"type": "markdown_text", "text": "start"},
			},
		},
		{
			"channel": "C2",
			"ts":      "123.4",
			"chunks": []any{
				map[string]any{"type": "markdown_text", "text": "delta"},
			},
		},
		{
			"channel": "C2",
			"ts":      "123.4",
			"chunks": []any{
				map[string]any{"type": "markdown_text", "text": "final"},
			},
		},
	}, bodies)
}

func TestHTTPClient_StartStreamOmitsRecipientFieldsForMultiPersonDirectMessage(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat.startStream", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write([]byte(`{"ok":true,"channel":"G1","ts":"123.4"}`))
	}))
	defer server.Close()
	client := NewHTTPClient("xoxb-token")
	client.baseURL = server.URL
	target := pkgslack.Target{
		ChannelID:       "G1",
		ThreadTS:        "100.1",
		ChannelType:     "mpim",
		RecipientUserID: "U1",
		RecipientTeamID: "T1",
	}

	stream, err := client.StartStream(context.Background(), target, "start")

	require.NoError(t, err)
	require.Equal(t, pkgslack.Stream{ChannelID: "G1", TS: "123.4"}, stream)
	require.Equal(t, map[string]any{
		"channel":   "G1",
		"thread_ts": "100.1",
		"chunks": []any{
			map[string]any{"type": "markdown_text", "text": "start"},
		},
	}, gotBody)
}

func TestHTTPClient_StartStreamDefaultsResponseChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"ts":"123.4"}`))
	}))
	defer server.Close()
	client := NewHTTPClient("xoxb-token")
	client.baseURL = server.URL

	stream, err := client.StartStream(context.Background(), pkgslack.Target{ChannelID: "C1"}, "")

	require.NoError(t, err)
	require.Equal(t, pkgslack.Stream{ChannelID: "C1", TS: "123.4"}, stream)
}

func TestHTTPClient_StartStreamReturnsCallError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
	}))
	defer server.Close()
	client := NewHTTPClient("xoxb-token")
	client.baseURL = server.URL

	_, err := client.StartStream(context.Background(), pkgslack.Target{ChannelID: "C1"}, "")

	require.EqualError(t, err, "invalid_arguments")
}

func TestHTTPClient_ReturnsSlackErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "rate limited", statusCode: http.StatusTooManyRequests, body: `{"ok":false,"error":"rate_limited"}`, want: "slack api rate limited"},
		{name: "http error", statusCode: http.StatusBadGateway, body: `{"ok":true}`, want: "slack api http status 502"},
		{name: "ok false with message", statusCode: http.StatusOK, body: `{"ok":false,"error":"invalid_auth"}`, want: "invalid_auth"},
		{name: "ok false without message", statusCode: http.StatusOK, body: `{"ok":false}`, want: "slack api returned ok=false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := NewHTTPClient("xoxb-token")
			client.baseURL = server.URL

			_, err := client.PostMessage(context.Background(), pkgslack.Target{ChannelID: "C1"}, "hello")

			require.EqualError(t, err, tt.want)
		})
	}
}

func TestHTTPClient_ReturnsJSONDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()
	client := NewHTTPClient("xoxb-token")
	client.baseURL = server.URL

	_, err := client.PostMessage(context.Background(), pkgslack.Target{ChannelID: "C1"}, "hello")

	require.Error(t, err)
}

func TestHTTPClient_ReturnsMarshalError(t *testing.T) {
	client := NewHTTPClient("xoxb-token")

	err := client.call(context.Background(), "chat.postMessage", make(chan int), nil)

	require.Error(t, err)
}

func TestHTTPClient_ReturnsRequestBuildAndTransportErrors(t *testing.T) {
	t.Run("request build", func(t *testing.T) {
		client := NewHTTPClient("xoxb-token")
		client.baseURL = "http://[::1"

		_, err := client.PostMessage(context.Background(), pkgslack.Target{ChannelID: "C1"}, "hello")

		require.Error(t, err)
	})

	t.Run("transport", func(t *testing.T) {
		client := NewHTTPClient("xoxb-token")
		client.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errSlackTest
		})}

		_, err := client.PostMessage(context.Background(), pkgslack.Target{ChannelID: "C1"}, "hello")

		require.ErrorIs(t, err, errSlackTest)
	})
}

func TestHTTPClient_ReturnsReadError(t *testing.T) {
	client := NewHTTPClient("xoxb-token")
	client.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       errReadCloser{},
			Header:     make(http.Header),
		}, nil
	})}

	_, err := client.PostMessage(context.Background(), pkgslack.Target{ChannelID: "C1"}, "hello")

	require.ErrorIs(t, err, errSlackTest)
}

func TestHTTPClient_DefaultsClientAndBaseURL(t *testing.T) {
	origTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = origTransport })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, defaultSlackAPIBase+"/chat.postMessage", r.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"ts":"123.4"}`)),
			Header:     make(http.Header),
		}, nil
	})
	client := &HTTPClient{token: "xoxb-token"}

	ts, err := client.PostMessage(context.Background(), pkgslack.Target{ChannelID: "C1"}, "hello")

	require.NoError(t, err)
	require.Equal(t, "123.4", ts)
}

func TestHTTPClient_RequiresClient(t *testing.T) {
	err := (*HTTPClient)(nil).AppendStream(context.Background(), pkgslack.Stream{}, []pkgslack.Chunk{
		pkgslack.MarkdownTextChunk("hello"),
	})

	require.EqualError(t, err, "slack client is required")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) {
	return 0, errSlackTest
}

func (errReadCloser) Close() error {
	return nil
}
