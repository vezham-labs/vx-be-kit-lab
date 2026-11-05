package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

const deepSeekSearchSetup = "Connect your DeepSeek API key in AI settings and enable Leo web search. Search API charges apply; no language model is required."

func (r *Runtime) searchDeepSeek(ctx context.Context, client *http.Client, principal c.Principal, query string) (c.WebSearchResult, error) {
	if principal.ID == "" {
		return c.WebSearchResult{}, ErrForbidden
	}
	service, err := r.credentials()
	if err != nil {
		return c.WebSearchResult{}, err
	}
	if _, err = service.definition("deepseek"); err != nil {
		return c.WebSearchResult{}, errors.New(deepSeekSearchSetup)
	}
	lock := service.credentialLock(principal.ID, "deepseek")
	lock.Lock()
	saved, err := service.load(ctx, principal.ID, "deepseek")
	if err != nil || !saved.Settings.Connected || !saved.Settings.SearchEnabled {
		lock.Unlock()
		return c.WebSearchResult{}, errors.New(deepSeekSearchSetup)
	}
	key, err := service.open(principal.ID, "deepseek", saved.Ciphertext)
	lock.Unlock()
	if err != nil {
		return c.WebSearchResult{}, errors.New("Reconnect your DeepSeek API key in AI settings.")
	}
	return searchDeepSeek(ctx, client, key, query)
}

// Search receives only the query. Page retrieval stays in the existing public
// HTTP reader, so this adapter never calls the separately billed Fetch API.
func searchDeepSeek(ctx context.Context, client *http.Client, key, query string) (c.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return c.WebSearchResult{}, errors.New("search needs a query")
	}
	payload, _ := json.Marshal(struct {
		Query       string `json:"query"`
		Count       int    `json:"count"`
		NeedContent bool   `json:"need_content"`
	}{leoQueryLimit(query, 128), 6, false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.deepseek.com/v1/tools/search", bytes.NewReader(payload))
	if err != nil {
		return c.WebSearchResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return c.WebSearchResult{}, ctx.Err()
		}
		return c.WebSearchResult{}, errors.New("Unable to reach DeepSeek search. Try again when the connection is available.")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return c.WebSearchResult{}, errors.New("DeepSeek rejected the saved API key. Reconnect DeepSeek in AI settings.")
	case http.StatusPaymentRequired:
		return c.WebSearchResult{}, errors.New("DeepSeek search has insufficient account balance. Check your DeepSeek billing.")
	case http.StatusTooManyRequests:
		return c.WebSearchResult{}, errors.New("DeepSeek search is rate limited. Wait before searching again.")
	default:
		return c.WebSearchResult{}, fmt.Errorf("DeepSeek search is unavailable (HTTP %d).", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 524289))
	if err != nil {
		return c.WebSearchResult{}, errors.New("Unable to read the DeepSeek search response.")
	}
	if len(data) > 524288 {
		return c.WebSearchResult{}, errors.New("DeepSeek search response exceeds the retrieval budget.")
	}
	var body struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &body) != nil || body.Results == nil {
		return c.WebSearchResult{}, errors.New("DeepSeek returned an unreadable search response.")
	}
	result := c.WebSearchResult{Sources: []c.WebSource{}}
	seen := map[string]bool{}
	for _, item := range body.Results {
		target, err := webURL(item.URL)
		if err != nil || seen[target.String()] {
			continue
		}
		seen[target.String()] = true
		result.Sources = append(result.Sources, sourceFor(target.String(), item.Title, item.Snippet))
		if len(result.Sources) == 6 {
			break
		}
	}
	return result, nil
}
