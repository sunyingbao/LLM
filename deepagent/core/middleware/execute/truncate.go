package execute

import "strings"

func truncateHeadTail(output string, maxBytes int) string {
	if maxBytes <= 0 || len(output) <= maxBytes {
		return output
	}
	marker := "\n... output truncated ...\n"
	if maxBytes <= len(marker)+2 {
		return output[:maxBytes]
	}
	remain := maxBytes - len(marker)
	head := remain * 7 / 10
	tail := remain - head
	var b strings.Builder
	b.Grow(maxBytes)
	b.WriteString(output[:head])
	b.WriteString(marker)
	b.WriteString(output[len(output)-tail:])
	return b.String()
}
