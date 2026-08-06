package media

import "strconv"

func parsePositiveID(value string, fallback int64) int64 {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return fallback
	}
	return id
}
