package inbox

type Message struct {
	V            int      `json:"v"`
	EventID      string   `json:"event_id"`
	InnerID      string   `json:"inner_id,omitempty"`
	From         string   `json:"from"`
	Kind         int      `json:"kind"`
	Content      string   `json:"content"`
	RumorAt      int64    `json:"rumor_at"`
	ReceivedAt   int64    `json:"received_at"`
	Relays       []string `json:"relays,omitempty"`
	Malformed    bool     `json:"malformed,omitempty"`
	RejectReason string   `json:"reject_reason,omitempty"`
}

type Sent struct {
	V           int      `json:"v"`
	EventID     string   `json:"event_id"`
	SelfEventID string   `json:"self_event_id"`
	InnerID     string   `json:"inner_id"`
	To          string   `json:"to"`
	Kind        int      `json:"kind"`
	Content     string   `json:"content"`
	RumorAt     int64    `json:"rumor_at"`
	SentAt      int64    `json:"sent_at"`
	AcceptedBy  []string `json:"accepted_by"`
	Final       bool     `json:"final,omitempty"`
}
