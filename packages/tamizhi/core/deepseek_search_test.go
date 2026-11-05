package core

import (
	"context"
	"encoding/json"
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deepSeekFixtureClient(t *testing.T, searches *int) *http.Client {
	t.Helper()
	return &http.Client{Transport: webFixtureTransport(func(req *http.Request) (*http.Response, error) {
		status, body := 200, ""
		content := "application/json"
		switch req.URL.Host + req.URL.Path {
		case "api.deepseek.com/models":
			body = `{"data":[{"id":"deepseek-chat"}]}`
		case "api.deepseek.com/v1/tools/search":
			*searches++
			if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer fixture-search-key" {
				t.Error("search authentication missing")
			}
			var args struct {
				Query       string `json:"query"`
				Count       int    `json:"count"`
				NeedContent bool   `json:"need_content"`
			}
			if json.NewDecoder(req.Body).Decode(&args) != nil || args.Query != "leo movie" || args.Count != 6 || args.NeedContent {
				t.Error("unexpected search request", args)
			}
			body = `{"results":[{"url":"https://movies.example/leo#cast","title":"Leo movie","snippet":"Snippet alone must not be the answer."},{"url":"https://movies.example/leo","title":"Duplicate"},{"url":"http://unsafe.example/","title":"HTTP"}]}`
		case "movies.example/leo":
			content = "text/html"
			body = `<title>Leo movie source</title><main><p>Leo movie fixture details were read from the source page.</p></main>`
			if req.Header.Get("Authorization") != "" {
				t.Error("provider credential leaked to source")
			}
		default:
			t.Errorf("unexpected external request: %s%s", req.URL.Host, req.URL.Path)
			status = 500
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {content}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
}
func configureSearchFixture(t *testing.T, runtime *Runtime, client *http.Client, owner c.Principal) {
	t.Helper()
	runtime.options.BYOKHTTPClient = client
	if _, err := runtime.SaveProviderKey(context.Background(), owner, "deepseek", "fixture-search-key"); err != nil {
		t.Fatal(err)
	}
}
func TestDeepSeekSearchRequiresOwnerOptInAndReadsSources(t *testing.T) {
	runtime, store := mountLeo(t)
	owner := c.Principal{ID: "search-owner"}
	searches := 0
	client := deepSeekFixtureClient(t, &searches)
	configureSearchFixture(t, runtime, client, owner)
	for _, principal := range []c.Principal{owner, {ID: "another-owner"}} {
		if _, err := runtime.searchDeepSeek(context.Background(), client, principal, "leo movie"); err == nil {
			t.Fatal("search bypassed opt-in")
		}
	}
	if searches != 0 {
		t.Fatal("connecting key made paid search calls")
	}
	saved, err := runtime.SetProviderSearch(context.Background(), owner, "deepseek", true)
	if err != nil || !saved.SearchEnabled {
		t.Fatal(saved, err)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "fixture-search-key") {
		t.Fatal("settings leaked key")
	}
	if _, err = runtime.searchDeepSeek(context.Background(), client, c.Principal{ID: "another-owner"}, "leo movie"); err == nil {
		t.Fatal("owner boundary bypassed")
	}
	if err = runtime.Mount(WebPlugin(WebOptions{Client: client}), true); err != nil {
		t.Fatal(err)
	}
	input := request("deepseek-research")
	input.Model = LeoModelID
	input.Messages[0].Content = "leo movie"
	run, err := runtime.StartRun(owner, input)
	if err != nil || waitRun(t, store, owner.ID, run.ID).Status != "complete" {
		t.Fatal(run, err)
	}
	events, _ := store.RunEvents(context.Background(), owner.ID, run.ID, 0)
	names := []string{}
	answer := ""
	for _, event := range events {
		if event.Type == "tool" {
			names = append(names, event.Name)
		}
		if event.Type == "text" {
			answer += event.Delta
		}
	}
	if searches != 1 || strings.Join(names, ",") != "web_search,web_open,web_read" || !strings.Contains(answer, "read from the source page") || !strings.Contains(answer, "https://movies.example/leo") || strings.Contains(answer, "Snippet alone") {
		t.Fatal(names, answer, searches)
	}
	if _, err = runtime.SetProviderSearch(context.Background(), owner, "deepseek", false); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.searchDeepSeek(context.Background(), client, owner, "leo movie"); err == nil || searches != 1 {
		t.Fatal("disabled search sent a request")
	}
	if _, err = runtime.SetProviderSearch(context.Background(), owner, "openai", true); err == nil {
		t.Fatal("unsupported provider enabled")
	}
	if err = runtime.DeleteProviderKey(context.Background(), owner, "deepseek"); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.SetProviderSearch(context.Background(), owner, "deepseek", true); err == nil {
		t.Fatal("disconnected provider enabled")
	}
}
func TestDeepSeekSearchFailureBoundaries(t *testing.T) {
	for _, example := range []struct {
		status        int
		body, problem string
	}{
		{401, "fixture-search-key", "saved API key"}, {402, "fixture-search-key", "balance"}, {429, "fixture-search-key", "rate limited"}, {503, "fixture-search-key", "unavailable"},
		{200, "not json", "unreadable"}, {200, `{"results":null}`, "unreadable"}, {200, strings.Repeat("x", 524289), "budget"},
	} {
		client := &http.Client{Transport: webFixtureTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: example.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(example.body)), Request: req}, nil
		})}
		_, err := searchDeepSeek(context.Background(), client, "fixture-search-key", "leo movie")
		if err == nil || !strings.Contains(err.Error(), example.problem) || strings.Contains(err.Error(), "fixture-search-key") {
			t.Fatal(example.status, err)
		}
	}
	calls := 0
	client := &http.Client{Transport: webFixtureTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://other.example/"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	if _, err := searchDeepSeek(context.Background(), client, "fixture-search-key", "leo movie"); err == nil || calls != 1 {
		t.Fatal("redirect followed", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := searchDeepSeek(ctx, publicHTTPClient(), "fixture-search-key", "leo movie"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestDeepSeekSearchEmptyAndUnicodeQuery(t *testing.T) {
	client := &http.Client{Transport: webFixtureTransport(func(req *http.Request) (*http.Response, error) {
		var args struct {
			Query string `json:"query"`
		}
		if json.NewDecoder(req.Body).Decode(&args) != nil || len([]rune(args.Query)) != 128 {
			t.Error("query limit is not Unicode safe")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"results":[]}`)), Request: req}, nil
	})}
	result, err := searchDeepSeek(context.Background(), client, "fixture-search-key", strings.Repeat("தமிழ்", 100))
	if err != nil || len(result.Sources) != 0 {
		t.Fatal(result, err)
	}
}
func TestProviderSearchHTTPSettingIsOwnerScoped(t *testing.T) {
	runtime, _ := mountLeo(t)
	owner := c.Principal{ID: "http-search-owner"}
	searches := 0
	configureSearchFixture(t, runtime, deepSeekFixtureClient(t, &searches), owner)
	api := NewAPI(runtime, HTTPOptions{Authenticate: AuthFunc(func(*http.Request) (c.Principal, error) { return owner, nil })})
	req := httptest.NewRequest("PUT", "/api/tamizhi/providers/deepseek/search", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	api.ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"searchEnabled":true`) || searches != 0 || strings.Contains(out.Body.String(), "fixture-search-key") {
		t.Fatal(out.Code, out.Body.String())
	}
}
