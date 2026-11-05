package core

import (
	"context"
	"encoding/json"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLeoPlainWebRequests(t *testing.T) {
	for _, prompt := range []string{"leo movie", "Leo movie review", "What is the latest React version?", "Who directed Leo movie?", "Please search for leo movie", "Can you tell me about leo movie?", "Hi Leo, what is the latest React version?", "help me find leo movie"} {
		t.Run(prompt, func(t *testing.T) {
			query, direct, matched := leoWebQuery(leoPlainPrompt(prompt))
			if !matched || query == "" || direct != "" {
				t.Fatal(query, direct, matched)
			}
		})
	}
	for _, prompt := range []string{"What is my password?", "What is our revenue?", "Tell me about our revenue", "Give me information about my customers", "my customer account", "write a movie script", "buy movie tickets", "send this to our customer"} {
		if query, _, matched := leoWebQuery(leoPlainPrompt(prompt)); matched {
			t.Fatal("private/action request became public search", prompt, query)
		}
	}
	for _, prompt := range []string{"https://movies.example/leo", "Please read this https://movies.example/leo", "Can you read https://movies.example/leo"} {
		_, direct, matched := leoWebQuery(leoPlainPrompt(prompt))
		if !matched || direct != "https://movies.example/leo" {
			t.Fatal(prompt, direct)
		}
	}
}
func TestLeoPlainMemoryAndFriendlyFallback(t *testing.T) {
	for _, example := range []struct{ prompt, name, value string }{
		{"Please remember my launch is Friday", "remember", "my launch is Friday"},
		{"Can you remember that my launch is Friday", "remember", "my launch is Friday"},
		{"Show my notes about the launch", "recall", "the launch"},
		{"What do you remember about the launch?", "recall", "the launch"},
	} {
		name, value := leoMemoryIntent(leoPlainPrompt(example.prompt))
		if name != example.name || value != example.value {
			t.Fatal(example, name, value)
		}
	}
	for _, prompt := range []string{"help", "What can you do?", "something unclear"} {
		answer := ""
		err := (&LeoProvider{}).Stream(context.Background(), ProviderInput{Messages: []c.Message{{Role: "user", Content: prompt}}}, func(delta ProviderDelta) error { answer += delta.Text; return nil })
		if err != nil || answer == "" || strings.Contains(answer, "`/search") || strings.Contains(answer, "`/tools") {
			t.Fatal(answer, err)
		}
	}
}
func TestLeoMovieTopicRunsRealResearchWorkflow(t *testing.T) {
	runtime, store := mountLeo(t)
	client := &http.Client{Transport: webFixtureTransport(func(request *http.Request) (*http.Response, error) {
		body := `<title>Leo movie fixture</title><main><p>Leo movie fixture details come from this retrieved source.</p></main>`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	if err := runtime.Mount(WebPlugin(WebOptions{Client: client, Search: func(_ context.Context, _ *http.Client, query string) (c.WebSearchResult, error) {
		if query != "leo movie" {
			t.Error(query)
		}
		return c.WebSearchResult{Sources: []c.WebSource{sourceFor("https://movies.example/leo", "Leo movie fixture", "Movie details")}}, nil
	}}), true); err != nil {
		t.Fatal(err)
	}
	input := request("plain-movie")
	input.Model = LeoModelID
	input.Messages[0].Content = "leo movie"
	owner := c.Principal{ID: "movie-owner"}
	run, err := runtime.StartRun(owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if waitRun(t, store, owner.ID, run.ID).Status != "complete" {
		t.Fatal("research did not complete")
	}
	events, _ := store.RunEvents(context.Background(), owner.ID, run.ID, 0)
	names := []string{}
	answer := ""
	for _, event := range events {
		if event.Type == "text" {
			answer += event.Delta
		}
		if event.Type == "tool" {
			names = append(names, event.Name)
			if event.Name == "web_search" {
				raw, _ := json.Marshal(event.Input)
				var args struct {
					Query string `json:"query"`
				}
				_ = json.Unmarshal(raw, &args)
				if args.Query != "leo movie" {
					t.Fatal(args)
				}
			}
		}
	}
	if strings.Join(names, ",") != "web_search,web_open,web_read" || !strings.Contains(answer, "retrieved source") || !strings.Contains(answer, "https://movies.example/leo") {
		t.Fatal(names, answer)
	}
	if strings.Contains(answer, "Which task would you like") {
		t.Fatal("plain topic still reached old fallback")
	}
}
