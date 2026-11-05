package core

import "strings"

// Keep the original request text; strip only conversational address/politeness prefixes.
func leoPlainPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	for range 4 {
		lower := strings.ToLower(prompt)
		changed := false
		for _, prefix := range []string{"please ", "please, ", "help me ", "can you ", "could you ", "would you ", "will you ", "leo, ", "hi leo, ", "hello leo, ", "hey leo, ", "hi, ", "hello, ", "hey, "} {
			if strings.HasPrefix(lower, prefix) {
				prompt = strings.TrimSpace(prompt[len(prefix):])
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}
	return prompt
}
func leoWords(prompt string) []string {
	return strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '\'')
	})
}

// Inferred public lookups do not select app/private tasks or action requests.
func leoPublicLookup(prompt string) bool {
	words := leoWords(prompt)
	if len(words) == 0 || len(words) > 40 || len(prompt) > 300 {
		return false
	}
	private := map[string]bool{"my": true, "our": true, "mine": true, "me": true, "us": true, "customer": true, "customers": true, "password": true, "passwords": true, "credential": true, "credentials": true, "secret": true, "secrets": true, "internal": true, "private": true, "workspace": true, "pipeline": true, "revenue": true}
	for _, word := range words {
		if private[word] {
			return false
		}
	}
	for _, marker := range []string{"@", "sk-", "api_key", "api key", "access token", "bearer "} {
		if strings.Contains(strings.ToLower(prompt), marker) {
			return false
		}
	}
	switch words[0] {
	case "what", "what's", "who", "who's", "when", "where", "why", "how", "which":
		return true
	}
	if len(words) > 12 {
		return false
	}
	public := map[string]bool{"movie": true, "movies": true, "film": true, "films": true, "cast": true, "actor": true, "actress": true, "director": true, "song": true, "songs": true, "album": true, "music": true, "book": true, "novel": true, "author": true, "weather": true, "news": true, "version": true, "release": true, "releases": true, "software": true, "programming": true, "history": true, "science": true, "country": true, "capital": true, "sport": true, "sports": true}
	switch words[0] {
	case "write", "delete", "remove", "send", "create", "save", "remember", "book", "buy", "change", "update":
		return false
	}
	for _, word := range words {
		if public[word] {
			return true
		}
	}
	return false
}
func leoMemoryIntent(prompt string) (name, value string) {
	lower := strings.ToLower(prompt)
	for _, prefix := range []string{"remember that ", "remember ", "save this note: ", "save this note ", "make a note that "} {
		if strings.HasPrefix(lower, prefix) {
			return "remember", strings.TrimSpace(prompt[len(prefix):])
		}
	}
	for _, prefix := range []string{"what do you remember about ", "what did i tell you about ", "find my notes about ", "show my notes about ", "recall "} {
		if strings.HasPrefix(lower, prefix) {
			return "recall", strings.TrimRight(strings.TrimSpace(prompt[len(prefix):]), "?!")
		}
	}
	return "", ""
}
