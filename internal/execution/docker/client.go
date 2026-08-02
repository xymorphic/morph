package docker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/moby/moby/client"
)

type Client struct {
	engine *client.Client
}

var newDockerClient = client.New

func NewClient(endpoint string) (*Client, error) {
	endpoint, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	engine, err := newDockerClient(client.WithHost(endpoint))
	if err != nil {
		return nil, err
	}
	return &Client{engine: engine}, nil
}

func (c *Client) Ping(ctx context.Context) (client.PingResult, error) {
	if c == nil || c.engine == nil {
		return client.PingResult{}, errors.New("docker client is required")
	}
	return c.engine.Ping(ctx, client.PingOptions{})
}

func (c *Client) Engine() *client.Client {
	if c == nil {
		return nil
	}
	return c.engine
}

func (c *Client) Close() error {
	if c == nil || c.engine == nil {
		return nil
	}
	return c.engine.Close()
}

func normalizeEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case strings.HasPrefix(strings.ToLower(endpoint), "npipe://"):
		return endpoint, nil
	case strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\`):
		return "npipe:////./pipe/" + strings.TrimPrefix(endpoint, `\\.\pipe\`), nil
	case strings.HasPrefix(strings.ToLower(endpoint), `//./pipe/`):
		return "npipe://" + endpoint, nil
	case strings.HasPrefix(endpoint, "unix://"):
		path := strings.TrimPrefix(endpoint, "unix://")
		if !filepath.IsAbs(path) {
			return "", errors.New("docker endpoint must use an absolute local socket")
		}
		return "unix://" + filepath.Clean(path), nil
	case strings.HasPrefix(endpoint, "/"):
		return "unix://" + filepath.Clean(endpoint), nil
	default:
		return "", errors.New("docker endpoint must be a local Unix socket or named pipe")
	}
}
