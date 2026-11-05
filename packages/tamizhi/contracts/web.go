package contracts

// WebSource is factual provenance, never an instruction or permission grant.
type WebSource struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Domain      string `json:"domain"`
	RetrievedAt string `json:"retrievedAt"`
	Snippet     string `json:"snippet,omitempty"`
}
type WebDocument struct {
	PageID      string      `json:"pageId,omitempty"`
	URL         string      `json:"url,omitempty"`
	Title       string      `json:"title,omitempty"`
	Content     string      `json:"content,omitempty"`
	Headings    []string    `json:"headings,omitempty"`
	Sources     []WebSource `json:"sources,omitempty"`
	RetrievedAt string      `json:"retrievedAt,omitempty"`
}
type WebSearchResult struct {
	Sources []WebSource `json:"sources"`
}
