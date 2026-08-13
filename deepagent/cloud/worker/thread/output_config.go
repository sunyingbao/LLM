//go:build !windows

package thread

// EventLogOption overrides whether one CloudAgent protocol event type should be
// persisted into the host EventLog.
type EventLogOption struct {
	Persist bool `json:"persist" yaml:"persist"`
}

// OutputConfig controls worker output delivery hints. EventLogOptions keys are
// CloudAgent protocol event type strings, for example "TOOL_CALL_STARTED".
// Missing keys keep the SDK default policy; unknown keys simply never match an
// emitted event type.
type OutputConfig struct {
	EventLogOptions map[string]EventLogOption `json:"event_log_options" yaml:"event_log_options"`
}

func cloneOutputConfig(cfg OutputConfig) OutputConfig {
	if len(cfg.EventLogOptions) == 0 {
		return OutputConfig{}
	}
	out := OutputConfig{EventLogOptions: make(map[string]EventLogOption, len(cfg.EventLogOptions))}
	for key, opt := range cfg.EventLogOptions {
		out.EventLogOptions[key] = opt
	}
	return out
}

func (cfg OutputConfig) eventLogOption(eventType string) (EventLogOption, bool) {
	if len(cfg.EventLogOptions) == 0 {
		return EventLogOption{}, false
	}
	opt, ok := cfg.EventLogOptions[eventType]
	return opt, ok
}
