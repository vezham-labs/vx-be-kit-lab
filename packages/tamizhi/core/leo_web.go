package core

import (
	"encoding/json"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"strings"
)

func leoWebQuery(prompt string) (query, direct string, matched bool) {
	prompt = leoPlainPrompt(prompt)
	lower := strings.ToLower(prompt)
	switch {
	case strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://"):
		return prompt, prompt, true
	case strings.HasPrefix(lower, "read this page "):
		direct = strings.TrimSpace(prompt[len("read this page "):])
		return direct, direct, true
	case strings.HasPrefix(lower, "read this "):
		direct = strings.TrimSpace(prompt[len("read this "):])
		return direct, direct, true
	case strings.HasPrefix(lower, "/read "):
		direct = strings.TrimSpace(prompt[len("/read "):])
		return direct, direct, true
	case strings.HasPrefix(lower, "read https://"):
		direct = strings.TrimSpace(prompt[len("read "):])
		return direct, direct, true
	}
	for _, prefix := range []string{"/search ", "search for ", "search ", "look up ", "research ", "find information about "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(prompt[len(prefix):]), "", true
		}
	}
	for _, prefix := range []string{"tell me about ", "give me information about ", "give me info on ", "i want to know about "} {
		if strings.HasPrefix(lower, prefix) {
			topic := strings.TrimSpace(prompt[len(prefix):])
			if topic != "" && leoPublicLookup("what is "+topic) {
				return topic, "", true
			}
			return "", "", false
		}
	}
	if leoPublicLookup(prompt) {
		return prompt, "", true
	}
	return "", "", false
}
func leoWeb(turn LeoTurn) (LeoStep, bool) {
	query, direct, matched := leoWebQuery(turn.Prompt)
	if !matched {
		return LeoStep{}, false
	}
	sources := []c.WebSource{}
	if direct != "" {
		if _, err := webURL(direct); err != nil {
			return LeoStep{Text: "Send one public HTTPS page URL to read. Private/local addresses and browser-only pages cannot be read."}, true
		}
		sources = append(sources, c.WebSource{URL: direct})
	} else {
		if !leoHasTool(turn, "web_search") {
			return leoCall(turn, "web_search", map[string]string{"query": query}), true
		}
		if len(turn.Results) == 0 {
			return leoCall(turn, "web_search", map[string]string{"query": leoQueryLimit(query, 300)}), true
		}
		var found c.WebSearchResult
		if json.Unmarshal(turn.Results[0].Output, &found) != nil || leoError(turn.Results[0].Output) != "" {
			return LeoStep{Text: leoFormatResult(turn.Results[0])}, true
		}
		seen := map[string]bool{}
		for _, source := range found.Sources {
			if _, err := webURL(source.URL); err == nil && !seen[source.URL] {
				sources = append(sources, source)
				seen[source.URL] = true
				if len(sources) == 2 {
					break
				}
			}
		}
		if len(sources) == 0 {
			return LeoStep{Text: "The search tool found no readable public sources. Try a more specific search."}, true
		}
	}
	reads := []LeoToolResult{}
	failures := []string{}
	for _, source := range sources {
		var opened *LeoToolResult
		for i := range turn.Results {
			result := &turn.Results[i]
			var args struct {
				URL string `json:"url"`
			}
			if result.Name == "web_open" && json.Unmarshal(result.Input, &args) == nil && args.URL == source.URL {
				opened = result
				break
			}
		}
		if opened == nil {
			return leoCall(turn, "web_open", map[string]string{"url": source.URL}), true
		}
		if problem := leoError(opened.Output); problem != "" {
			failures = append(failures, problem)
			continue
		}
		var page c.WebDocument
		if json.Unmarshal(opened.Output, &page) != nil || page.PageID == "" {
			failures = append(failures, "The web tool did not return a readable page handle.")
			continue
		}
		var read *LeoToolResult
		for i := range turn.Results {
			result := &turn.Results[i]
			var args struct {
				PageID string `json:"pageId"`
			}
			if result.Name == "web_read" && json.Unmarshal(result.Input, &args) == nil && args.PageID == page.PageID {
				read = result
				break
			}
		}
		if read == nil {
			return leoCall(turn, "web_read", map[string]string{"pageId": page.PageID, "query": leoQueryLimit(query, 300)}), true
		}
		if problem := leoError(read.Output); problem != "" {
			failures = append(failures, problem)
			continue
		}
		reads = append(reads, *read)
	}
	answer := leoWebAnswer(reads)
	if len(failures) > 0 {
		answer += "\n\nSome sources could not be read:\n\n" + leoCode(leoLimit(strings.Join(failures, "\n"), 1000))
	}
	return LeoStep{Text: answer}, true
}

func leoQueryLimit(value string, limit int) string {
	runes := []rune(value)
	return string(runes[:min(limit, len(runes))])
}
func leoWebAnswer(reads []LeoToolResult) string {
	var answer strings.Builder
	answer.WriteString("From the pages I read:\n\n")
	count := 0
	for _, read := range reads {
		var page c.WebDocument
		if json.Unmarshal(read.Output, &page) != nil || strings.TrimSpace(page.Content) == "" || len(page.Sources) == 0 {
			continue
		}
		source := page.Sources[0]
		if _, err := webURL(source.URL); err != nil {
			continue
		}
		title := source.Title
		if title == "" {
			title = source.Domain
		}
		target := strings.NewReplacer("<", "%3C", ">", "%3E").Replace(source.URL)
		answer.WriteString("[" + leoEscape(title) + "](<" + target + ">)\n\n")
		for _, line := range strings.Split(leoLimit(page.Content, 1200), "\n") {
			answer.WriteString("> " + leoEscape(line) + "\n")
		}
		answer.WriteString("\n")
		count++
	}
	if count == 0 {
		return "The web tools did not return readable source content. Try another URL or a more specific search."
	}
	return answer.String()
}
func leoEscape(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "!", "\\!", "#", "\\#", "|", "\\|")
	return replacer.Replace(value)
}
