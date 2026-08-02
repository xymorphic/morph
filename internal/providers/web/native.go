package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/pkg/fetch"
	"github.com/xymorphic/morph/pkg/str"
	"golang.org/x/net/html"
)

const (
	nativeDefaultTimeout = constants.NativeWebDefaultTimeout
	nativeUserAgent      = constants.NativeWebUserAgent
)

var (
	errNativeSearchUnsupported = errors.New("native web provider does not support search")

	nativeReadabilityFromReader = readability.FromReader

	nativeDefaultHTTPClient = func(provider *NativeProvider) *http.Client {
		return provider.newHTTPClient()
	}

	nativeDefaultDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := net.Dialer{Timeout: constants.NativeWebDialTimeout}
		return dialer.DialContext(ctx, network, address)
	}
)

type nativeFetchPolicy struct {
	hostPolicy   guardrails.HostPolicy
	websiteCheck func(context.Context, string) error
}

func (p nativeFetchPolicy) Check(ctx context.Context, parsed *url.URL) error {
	if parsed == nil {
		return nil
	}

	if block, blocked := p.hostPolicy.Check(parsed.Hostname()); blocked {
		return errors.New(block.Message)
	}

	if p.websiteCheck == nil {
		return nil
	}

	return p.websiteCheck(ctx, parsed.String())
}

// NativeProvider uses the built-in web search and extraction implementation.
type NativeProvider struct {
	client                   *http.Client
	makeClient               func() *http.Client
	newRequest               func(context.Context, string, string, io.Reader) (*http.Request, error)
	dial                     func(context.Context, string, string) (net.Conn, error)
	resolveHost              func(context.Context, string) ([]netip.Addr, error)
	hostPolicy               guardrails.HostPolicy
	maxExtractCharsPerResult int
	maxExtractResponseBytes  int
}

// NewNative returns the built-in web provider.
func NewNative(opts Options) (Provider, error) {
	opts = opts.Normalize()

	provider := &NativeProvider{
		resolveHost: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		hostPolicy: guardrails.NewHostPolicy(
			opts.NativeAllowedHosts,
			opts.NativeBlockedHosts,
			opts.NativeAllowedHostFiles,
			opts.NativeBlockedHostFiles,
		),
		maxExtractCharsPerResult: opts.MaxExtractCharPerResult,
		maxExtractResponseBytes:  opts.MaxExtractResponseBytes,
	}

	provider.client = provider.newHTTPClient()
	return provider, nil
}

func (p *NativeProvider) Search(context.Context, string, int) ([]SearchResult, error) {
	return nil, errNativeSearchUnsupported
}

func (p *NativeProvider) Extract(ctx context.Context, urls []string) ([]ExtractResult, error) {
	format := getExtractFormat(ctx, "text")
	maxChars := getExtractCharLimit(ctx, p.maxExtractCharsPerResult)
	results := make([]ExtractResult, 0, len(urls))

	for _, rawURL := range urls {
		rawURLValue := str.String(rawURL)
		result := p.extract(ctx, rawURLValue.Trim(), format, maxChars)
		results = append(results, result)
	}

	return results, nil
}

func (p *NativeProvider) extract(ctx context.Context, rawURL, format string, maxChars int) ExtractResult {
	result := ExtractResult{URL: rawURL, ContentFormat: format}

	validatedURL, err := p.fetcher().ValidateURL(ctx, rawURL)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	response, err := p.fetcher().Get(ctx, fetch.GetRequest{
		URL:        validatedURL.String(),
		Header:     http.Header{"Accept": []string{"text/html,text/plain;q=0.9"}, "User-Agent": []string{nativeUserAgent}},
		Timeout:    nativeDefaultTimeout,
		MaxBytes:   p.maxExtractResponseBytes,
		Client:     p.httpClient(),
		NewRequest: p.newRequest,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}

	extractionURL := validatedURL
	if response.FinalURL != "" {
		parsedFinalURL, parseErr := url.Parse(response.FinalURL)
		if parseErr == nil {
			extractionURL = parsedFinalURL
			result.URL = extractionURL.String()
		}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		statusValue := str.String(response.Status)
		status := statusValue.Trim()
		if status == "" {
			status = fmt.Sprintf("%d", response.StatusCode)
		}
		result.Error = fmt.Sprintf("web extraction request failed: %s", status)
		return result
	}
	getValue := str.String(response.Header.Get("Content-Type"))
	contentType := getValue.Trim()
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "" && mediaType != "text/html" && mediaType != "application/xhtml+xml" && mediaType != "text/plain" {
		result.Error = "unsupported content type: " + mediaType
		return result
	}

	var extracted nativeDocument
	if mediaType == "text/plain" {
		bodyValue := str.String(string(response.Body))
		extracted.Content = bodyValue.Trim()
	} else {
		extracted, err = extractNativeHTML(response.Body, extractionURL, format)
		if err != nil {
			result.Error = err.Error()
			return result
		}
	}

	content, truncated := truncateContent(extracted.Content, maxChars)
	result.Title = extracted.Title
	result.Content = content
	result.Truncated = truncated || response.Truncated
	result.DownloadTruncated = response.Truncated

	return result
}

func (p *NativeProvider) newHTTPClient() *http.Client {
	return p.fetcher().NewHTTPClient(nativeDefaultTimeout)
}

func (p *NativeProvider) fetcher() *fetch.Fetcher {
	dial := p.dial
	if dial == nil {
		dial = nativeDefaultDialContext
	}

	return fetch.New(
		fetch.WithResolveHost(p.resolveHost),
		fetch.WithDial(dial),
		fetch.WithPolicy(nativeFetchPolicy{
			hostPolicy: p.hostPolicy,
			websiteCheck: func(ctx context.Context, rawURL string) error {
				if block, blocked := getExtractWebsitePolicy(ctx).Check(rawURL); blocked {
					return errors.New(block.Message)
				}

				return nil
			},
		}),
	)
}

func readNativeResponse(body io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		data, err := io.ReadAll(body)
		return data, false, err
	}

	data, err := io.ReadAll(io.LimitReader(body, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}

	if len(data) > maxBytes {
		data = data[:maxBytes]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}

		return data, true, nil
	}

	return data, false, nil
}

func (p *NativeProvider) httpClient() *http.Client {
	if p.client != nil {
		return p.client
	}

	makeClient := p.makeClient
	if makeClient == nil {
		makeClient = func() *http.Client {
			return nativeDefaultHTTPClient(p)
		}
	}

	return makeClient()
}

type nativeDocument struct {
	Title   string
	Content string
}

func extractNativeHTML(data []byte, pageURL *url.URL, format string) (nativeDocument, error) {
	article, err := nativeReadabilityFromReader(bytes.NewReader(data), pageURL)
	if err != nil {
		return nativeDocument{}, err
	}

	var content strings.Builder

	if format == "markdown" {
		content.WriteString(renderNativeMarkdown(article.Node))
	} else {
		err = article.RenderText(&content)
	}

	if err != nil {
		return nativeDocument{}, err
	}
	titleValue := str.String(article.Title())
	textValue := str.String(content.String())
	return nativeDocument{
		Title:   titleValue.Trim(),
		Content: textValue.Trim(),
	}, nil
}

func renderNativeMarkdown(root *html.Node) string {
	lines := make([]string, 0, 16)
	appendLine := func(line string) {
		lineValue := str.String(line)
		line = lineValue.Trim()
		if line == "" {
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			return
		}
		lines = append(lines, line)
	}

	var walk func(*html.Node)

	walk = func(node *html.Node) {
		if node == nil {
			return
		}

		if node.Type == html.ElementNode {
			switch strings.ToLower(node.Data) {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				text := collectNativeText(node)
				if text != "" {
					appendLine("")
					appendLine(strings.Repeat("#", getNativeHeadingLevel(node.Data)) + " " + text)
					appendLine("")
				}
				return
			case "p", "blockquote":
				text := collectNativeText(node)
				if text != "" {
					appendLine(text)
					appendLine("")
				}
				return
			case "li":
				text := collectNativeText(node)
				if text != "" {
					appendLine("- " + text)
				}
				return
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}

	walk(root)
	joinValue := str.String(strings.Join(compactNativeLines(lines), "\n"))
	return joinValue.Trim()
}

func collectNativeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == html.TextNode {
		return strings.Join(strings.Fields(node.Data), " ")
	}

	parts := make([]string, 0, 4)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if text := collectNativeText(child); text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, " ")
}

func compactNativeLines(lines []string) []string {
	compacted := make([]string, 0, len(lines))
	blank := false

	for _, line := range lines {
		lineValue2 := str.String(line)
		line = lineValue2.Trim()
		if line == "" {
			if len(compacted) == 0 || blank {
				continue
			}

			blank = true
			compacted = append(compacted, "")
			continue
		}
		blank = false
		compacted = append(compacted, line)
	}

	for len(compacted) > 0 && compacted[len(compacted)-1] == "" {
		compacted = compacted[:len(compacted)-1]
	}

	return compacted
}

func getNativeHeadingLevel(name string) int {
	switch strings.ToLower(name) {
	case "h1":
		return 1
	case "h2":
		return 2
	case "h3":
		return 3
	case "h4":
		return 4
	case "h5":
		return 5
	default:
		return 6
	}
}
