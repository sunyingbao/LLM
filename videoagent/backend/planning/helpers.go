package planning

import "strings"

const defaultPromptTTSCPM = 280

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func normalizedDurationMS(duration int) int {
	if duration <= 0 {
		return 3000
	}
	if duration < 1000 {
		return duration * 1000
	}
	return duration
}
