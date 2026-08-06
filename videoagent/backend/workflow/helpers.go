package workflow

import (
	"encoding/json"
	"fmt"
)

func decodeJSON[T any](data json.RawMessage) (T, error) {
	var value T
	if len(data) == 0 {
		return value, fmt.Errorf("payload is empty")
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}
