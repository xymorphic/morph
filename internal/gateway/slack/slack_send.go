package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	slack "github.com/xymorphic/morph/pkg/gateway/slack"
	"github.com/xymorphic/morph/pkg/str"
)

const defaultSlackAPIBase = "https://slack.com/api"

type API interface {
	PostMessage(context.Context, slack.Target, string) (string, error)
	StartStream(context.Context, slack.Target, string) (slack.Stream, error)
	AppendStream(context.Context, slack.Stream, []slack.Chunk) error
	StopStream(context.Context, slack.Stream, string) error
}

type HTTPClient struct {
	client  *http.Client
	baseURL string
	token   string
}

type slackAPIError struct {
	Code string
}

func (e slackAPIError) Error() string {
	if e.Code == "" {
		return "slack api returned ok=false"
	}
	if e.Code == "rate_limited" {
		return "slack api rate limited"
	}
	if status, ok := strings.CutPrefix(e.Code, "http_status_"); ok {
		return "slack api http status " + status
	}

	return e.Code
}

func NewHTTPClient(token string) *HTTPClient {
	tokenValue := str.String(token)
	return &HTTPClient{client: http.DefaultClient, baseURL: defaultSlackAPIBase, token: tokenValue.Trim()}
}

func (c *HTTPClient) PostMessage(ctx context.Context, target slack.Target, text string) (string, error) {
	var result struct {
		TS string `json:"ts"`
	}
	if err := c.call(ctx, "chat.postMessage", slack.PostMessageRequest{
		Channel:  target.ChannelID,
		ThreadTS: target.ThreadTS,
		Text:     text,
	}, &result); err != nil {
		return "", err
	}

	return result.TS, nil
}

func (c *HTTPClient) StartStream(ctx context.Context, target slack.Target, text string) (slack.Stream, error) {
	var result struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	req := slack.StartStreamRequest{
		Channel:  target.ChannelID,
		ThreadTS: target.ThreadTS,
	}
	textValue := str.String(text)
	if textValue.Trim() != "" {
		req.Chunks = []slack.Chunk{slack.MarkdownTextChunk(text)}
	}
	channelTypeValue := str.String(target.ChannelType)
	if channelTypeValue.Trim() == "im" {
		req.RecipientUserID = target.RecipientUserID
		req.RecipientTeamID = target.RecipientTeamID
	}

	if err := c.call(ctx, "chat.startStream", req, &result); err != nil {
		return slack.Stream{}, err
	}
	if result.Channel == "" {
		result.Channel = target.ChannelID
	}

	return slack.Stream{ChannelID: result.Channel, TS: result.TS}, nil
}

func (c *HTTPClient) AppendStream(ctx context.Context, stream slack.Stream, chunks []slack.Chunk) error {
	return c.call(ctx, "chat.appendStream", slack.AppendStreamRequest{
		Channel: stream.ChannelID,
		TS:      stream.TS,
		Chunks:  chunks,
	}, nil)
}

func (c *HTTPClient) StopStream(ctx context.Context, stream slack.Stream, text string) error {
	req := slack.StopStreamRequest{Channel: stream.ChannelID, TS: stream.TS}
	textValue2 := str.String(text)
	if textValue2.Trim() != "" {
		req.Chunks = []slack.Chunk{slack.MarkdownTextChunk(text)}
	}

	return c.call(ctx, "chat.stopStream", req, nil)
}

func (c *HTTPClient) call(ctx context.Context, method string, req any, out any) error {
	if c == nil {
		return errors.New("slack client is required")
	}

	if c.client == nil {
		c.client = http.DefaultClient
	}
	baseURL := strings.TrimRight(c.baseURL, "/")
	if baseURL == "" {
		baseURL = defaultSlackAPIBase
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	tokenValue2 := str.String(c.token)
	httpReq.Header.Set("Authorization", "Bearer "+tokenValue2.Trim())
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return err
	}
	var apiResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return err
	}
	if httpResp.StatusCode == http.StatusTooManyRequests {
		return slackAPIError{Code: "rate_limited"}
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return slackAPIError{Code: fmt.Sprintf("http_status_%d", httpResp.StatusCode)}
	}
	if !apiResp.OK {
		return slackAPIError{Code: apiResp.Error}
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}

	return nil
}
