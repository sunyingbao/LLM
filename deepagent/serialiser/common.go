// Package serialiser contains shared data encoding helpers.
package serialiser

import (
	"code.byted.org/gopkg/logs/v2/log"
	"github.com/bytedance/sonic"
)

// ToStringIgnore encodes a value as JSON and returns an empty string on error.
func ToStringIgnore(value interface{}) (encoded string) {
	encoded, _ = sonic.MarshalString(value)
	return encoded
}

// ToString encodes a value as JSON, using an empty object for nil.
func ToString(value interface{}) (encoded string) {
	if value == nil {
		return "{}"
	}
	encoded, _ = sonic.MarshalString(value)
	return encoded
}

// ToBytes encodes a value as JSON bytes and ignores encoding errors.
func ToBytes(value interface{}) (encoded []byte) {
	encoded, _ = sonic.Marshal(value)
	return encoded
}

// FromString decodes a JSON string into target.
func FromString(value string, target interface{}) (err error) {
	err = sonic.Unmarshal([]byte(value), target)
	if err != nil {
		log.V2.Error().KVs("error_msg", "sonic.Unmarshal error", "arg", value).Error(err).Emit()
	}
	return err
}

// ToStruct decodes a JSON string into a typed value.
func ToStruct[T any](value string) (result T, err error) {
	if value == "" {
		return result, nil
	}
	err = sonic.UnmarshalString(value, &result)
	return result, err
}
