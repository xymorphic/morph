package web

import (
	"context"
	"strconv"
	"strings"
	"time"

	pkgcache "github.com/xymorphic/morph/pkg/cache"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

var webLog = logutils.Module("providers.web")

// CacheOptions configures the provider name, TTL, and clock used for web cache entries.
type CacheOptions struct {
	ProviderName string
	TTL          time.Duration
	Now          func() time.Time
}

type cachedProvider struct {
	provider     Provider
	providerName string
	search       *pkgcache.Cache[string, []SearchResult]
	extract      *pkgcache.Cache[string, ExtractResult]
}

// NewCachedProvider wraps provider with an in-memory response cache.
func NewCachedProvider(provider Provider, opts CacheOptions) Provider {
	if provider == nil || opts.TTL <= 0 {
		return provider
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	providerNameValue := str.String(opts.ProviderName)
	return &cachedProvider{
		provider:     provider,
		providerName: providerNameValue.Normalized(),
		search: pkgcache.New(pkgcache.Options[string, []SearchResult]{
			TTL:   opts.TTL,
			Now:   now,
			Clone: cloneSearchResults,
		}),
		extract: pkgcache.New(pkgcache.Options[string, ExtractResult]{
			TTL: opts.TTL,
			Now: now,
		}),
	}
}

func (p *cachedProvider) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if p == nil || p.provider == nil {
		return nil, ErrProviderNotConfigured
	}

	key := p.searchKey(query, count)
	if results, ok := p.cachedSearch(key); ok {
		p.logCacheHit("search")
		return results, nil
	}

	results, err := p.provider.Search(ctx, query, count)
	if err != nil {
		return nil, err
	}

	p.storeSearch(key, results)
	return cloneSearchResults(results), nil
}

func (p *cachedProvider) Extract(ctx context.Context, urls []string) ([]ExtractResult, error) {
	if p == nil || p.provider == nil {
		return nil, ErrProviderNotConfigured
	}
	if len(urls) == 0 {
		return nil, nil
	}

	results := make([]ExtractResult, len(urls))
	missURLs := make([]string, 0, len(urls))
	missIndexes := make([]int, 0, len(urls))
	missKeys := make([]string, 0, len(urls))
	for idx, rawURL := range urls {
		key := p.extractKey(ctx, rawURL)
		if result, ok := p.cachedExtract(key); ok {
			p.logCacheHit("extract")
			results[idx] = result
			continue
		}
		missURLs = append(missURLs, rawURL)
		missIndexes = append(missIndexes, idx)
		missKeys = append(missKeys, key)
	}

	if len(missURLs) == 0 {
		return results, nil
	}

	fetched, err := p.provider.Extract(ctx, missURLs)
	if err != nil {
		return nil, err
	}

	for idx, result := range fetched {
		if idx >= len(missIndexes) {
			break
		}

		results[missIndexes[idx]] = result
		errorValue := str.String(result.Error)
		if errorValue.Trim() == "" {
			p.storeExtract(missKeys[idx], result)
		}
	}
	for idx := len(fetched); idx < len(missIndexes); idx++ {
		missURLsValue := str.String(missURLs[idx])
		results[missIndexes[idx]] = ExtractResult{
			URL:   missURLsValue.Trim(),
			Error: "web extraction provider returned no result",
		}
	}

	return cloneExtractResults(results), nil
}

func (p *cachedProvider) cachedSearch(key string) ([]SearchResult, bool) {
	if p == nil || p.search == nil {
		return nil, false
	}

	return p.search.Get(key)
}

func (p *cachedProvider) storeSearch(key string, results []SearchResult) {
	if p == nil || p.search == nil {
		return
	}

	p.search.Set(key, results)
}

func (p *cachedProvider) cachedExtract(key string) (ExtractResult, bool) {
	if p == nil || p.extract == nil {
		return ExtractResult{}, false
	}

	return p.extract.Get(key)
}

func (p *cachedProvider) storeExtract(key string, result ExtractResult) {
	if p == nil || p.extract == nil {
		return
	}

	p.extract.Set(key, result)
}

func (p *cachedProvider) searchKey(query string, count int) string {
	queryValue := str.String(query)
	return strings.Join([]string{
		p.providerName, queryValue.
			Trim(), strconv.Itoa(count),
	}, "\x00")
}

func (p *cachedProvider) extractKey(ctx context.Context, rawURL string) string {
	opts := ExtractOptionsFromContext(ctx)
	rawURLValue := str.String(rawURL)
	return strings.Join([]string{
		p.providerName, rawURLValue.
			Trim(), opts.Format,
		strconv.Itoa(opts.MaxChars),
		opts.Query,
	}, "\x00")
}

func (p *cachedProvider) logCacheHit(operation string) {
	webLog.Info().
		Str("provider", p.providerName).
		Str("operation", operation).
		Msg("web provider cache hit")
}

func cloneSearchResults(results []SearchResult) []SearchResult {
	if results == nil {
		return nil
	}

	cloned := make([]SearchResult, len(results))
	copy(cloned, results)
	return cloned
}

func cloneExtractResults(results []ExtractResult) []ExtractResult {
	if results == nil {
		return nil
	}

	cloned := make([]ExtractResult, len(results))
	copy(cloned, results)
	return cloned
}
