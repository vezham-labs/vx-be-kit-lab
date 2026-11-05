package contracts

type CompanionSession struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// CompanionConnection is a short-lived handshake capability, bound to the browser's Origin.
type CompanionConnection struct {
	RequestID string `json:"requestId"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}
type CompanionConnectionRequest struct {
	RequestID string `json:"requestId"`
}
type CompanionConnectionResult struct {
	Status  string            `json:"status"`
	Session *CompanionSession `json:"session,omitempty"`
}
