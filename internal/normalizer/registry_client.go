package normalizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var ErrSchemaNotFound = errors.New("schema not found")

// RegistrySchema is the validation contract for one category.
type RegistrySchema struct {
	Category    string         `json:"category"`
	Version     int            `json:"version"`
	JSONSchema  map[string]any `json:"json_schema"`
	RoutingYAML string         `json:"routing_yaml"`
}

// RegistryClient fetches schemas from schema_registry_service with a short
// TTL cache, per ADR-003 (normalizer must not read git/Postgres directly and
// must survive brief registry blips via last-known schemas).
type RegistryClient struct {
	baseURL string
	token   string
	http    *http.Client
	ttl     time.Duration

	mu    sync.RWMutex
	cache map[string]cachedSchema
}

type cachedSchema struct {
	schema    RegistrySchema
	fetchedAt time.Time
}

func NewRegistryClient(baseURL, token string) *RegistryClient {
	return &RegistryClient{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 5 * time.Second},
		ttl:     30 * time.Second,
		cache:   map[string]cachedSchema{},
	}
}

func (c *RegistryClient) GetSchema(ctx context.Context, category string) (RegistrySchema, error) {
	c.mu.RLock()
	if cached, ok := c.cache[category]; ok && time.Since(cached.fetchedAt) < c.ttl {
		c.mu.RUnlock()
		return cached.schema, nil
	}
	c.mu.RUnlock()

	// category originates from event payloads; escape it so it cannot traverse the registry path.
	endpoint := c.baseURL + "/v1/schemas/" + url.PathEscape(category)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil) //nolint:gosec // baseURL is operator config; category is escaped above
	if err != nil {
		return RegistrySchema{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "SignalYard "+c.token)
	}
	resp, err := c.http.Do(req) //nolint:gosec // endpoint is operator-configured base URL with escaped category
	if err != nil {
		// Registry unreachable: fall back to last-known schema if we have one (ADR-003).
		c.mu.RLock()
		cached, ok := c.cache[category]
		c.mu.RUnlock()
		if ok {
			return cached.schema, nil
		}
		return RegistrySchema{}, fmt.Errorf("registry unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return RegistrySchema{}, ErrSchemaNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return RegistrySchema{}, fmt.Errorf("registry returned %d", resp.StatusCode)
	}
	var sc RegistrySchema
	if err := json.NewDecoder(resp.Body).Decode(&sc); err != nil {
		return RegistrySchema{}, err
	}
	c.mu.Lock()
	c.cache[category] = cachedSchema{schema: sc, fetchedAt: time.Now()}
	c.mu.Unlock()
	return sc, nil
}

// Invalidate drops a cached entry, used when a schema-approved event arrives.
func (c *RegistryClient) Invalidate(category string) {
	c.mu.Lock()
	delete(c.cache, category)
	c.mu.Unlock()
}
