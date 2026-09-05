package coordinator

type CancelInputControlPayload struct {
	ControlType     string `json:"control_type"`
	RequestID       string `json:"request_id"`
	ThreadID        int64  `json:"thread_id"`
	CutoffMessageID int64  `json:"cutoff_message_id"`
	Reason          string `json:"reason,omitempty"`
}

type CancelInputControlMetadata struct {
	ControlType      string `json:"control_type"`
	RequestID        string `json:"request_id"`
	CutoffMessageID  string `json:"cutoff_message_id"`
	Reason           string `json:"reason,omitempty"`
	LogID            string `json:"logid,omitempty"`
	BytedCtxMetaInfo string `json:"byted_ctx_meta_info,omitempty"`
	KEnv             string `json:"K_ENV,omitempty"`
}

type CloseThreadControlPayload struct {
	ControlType string `json:"control_type"`
	RequestID   string `json:"request_id"`
	ThreadID    int64  `json:"thread_id"`
	Reason      string `json:"reason,omitempty"`
}

type CloseThreadControlMetadata struct {
	ControlType      string `json:"control_type"`
	RequestID        string `json:"request_id"`
	Reason           string `json:"reason,omitempty"`
	LogID            string `json:"logid,omitempty"`
	BytedCtxMetaInfo string `json:"byted_ctx_meta_info,omitempty"`
	KEnv             string `json:"K_ENV,omitempty"`
}
