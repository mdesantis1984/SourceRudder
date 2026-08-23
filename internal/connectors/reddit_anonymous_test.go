package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

// TestRedditConfigHasNoOAuthFields is the Phase 4.3 RED gate. The
// RedditConfig struct MUST NOT carry OAuth surface area (ClientID,
// ClientSecret, AccessToken). Anonymous-only delivery means there is
// nothing for an operator to configure that pretends to authenticate.
// This test fails to compile (RED) while ClientID/ClientSecret still
// exist on RedditConfig.
func TestRedditConfigHasNoOAuthFields(t *testing.T) {
	rt := reflect.TypeOf(RedditConfig{})

	var forbidden []string
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		lname := strings.ToLower(name)
		if strings.Contains(lname, "clientid") ||
			strings.Contains(lname, "clientsecret") ||
			strings.Contains(lname, "accesstoken") ||
			strings.Contains(lname, "oauth") {
			forbidden = append(forbidden, name)
		}
	}
	if len(forbidden) > 0 {
		t.Fatalf("RedditConfig carries OAuth surface area: %v; anonymous-only delivery forbids these", forbidden)
	}
}

// TestRedditConnectorHasNoOAuthStorage ensures the connector struct
// itself does not retain an oauth bearer token. The previous delivery
// stored a redditOAuthToken and an oauthHost for oauth.reddit.com;
// both must be gone.
func TestRedditConnectorHasNoOAuthStorage(t *testing.T) {
	rt := reflect.TypeOf(RedditConnector{})

	var forbidden []string
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		lname := strings.ToLower(name)
		if strings.Contains(lname, "oauth") || strings.Contains(lname, "accesstoken") {
			forbidden = append(forbidden, name)
		}
	}
	if len(forbidden) > 0 {
		t.Fatalf("RedditConnector retains OAuth storage: %v; anonymous-only delivery forbids these", forbidden)
	}
}

// TestRedditConnectorNoHasOAuthMethod asserts the public HasOAuthCredentials
// method is gone. Callers that need the OAuth status should not be able
// to query it because the configuration seam no longer supports it.
func TestRedditConnectorNoHasOAuthMethod(t *testing.T) {
	rt := reflect.TypeOf(&RedditConnector{})
	if _, ok := rt.MethodByName("HasOAuthCredentials"); ok {
		t.Fatalf("RedditConnector.HasOAuthCredentials still exists; remove it for anonymous-only delivery")
	}
}

// TestRedditCacheKeySharedAcrossUAs is the Phase 4.1 working test.
// Two connectors with different UAs but the same query MUST collide on
// the same cache entry: one populates, the other reads.
func TestRedditCacheKeySharedAcrossUAs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"children":[]}}`))
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	populator := NewRedditConnector(RedditConfig{BaseURL: srv.URL, UserAgent: "ua-populator"}, cacheSvc)
	reader := NewRedditConnector(RedditConfig{BaseURL: srv.URL, UserAgent: "ua-reader"}, cacheSvc)

	req := &types.SearchRequest{Query: "same-query"}

	resp1, err := populator.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("populator Search: %v", err)
	}
	if resp1.Cached {
		t.Fatalf("first Search must NOT be a cache hit")
	}

	resp2, err := reader.Search(context.Background(), req)
	if err != nil {
		t.Fatalf("reader Search: %v", err)
	}
	if !resp2.Cached {
		t.Fatalf("second Search through a different UA MUST be a cache hit (key ignores UA)")
	}
}
