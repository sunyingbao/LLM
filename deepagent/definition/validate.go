package agentdefinition

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FieldError reports a stable validation failure at one definition field.
type FieldError struct {
	Field string
	Code  string
	Cause error
}

func (e *FieldError) Error() (message string) {
	if e == nil {
		return ""
	}
	message = fmt.Sprintf("invalid agent definition field %q: %s", e.Field, e.Code)
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *FieldError) Unwrap() (cause error) {
	if e == nil {
		return nil
	}
	cause = e.Cause
	return cause
}

// Validate checks only declarative shape. Provider resolution belongs to a runtime host.
func Validate(definition Definition) (err error) {
	if strings.TrimSpace(definition.Name) == "" {
		return newFieldError("name", "required", nil)
	}
	if strings.TrimSpace(definition.Version) == "" {
		return newFieldError("version", "required", nil)
	}
	if err = validateBindings("tools", definition.Tools); err != nil {
		return err
	}
	if err = validateBindings("middleware", definition.Middleware); err != nil {
		return err
	}
	if definition.Limits.MaxSteps < 0 {
		return newFieldError("limits.max_steps", "must_not_be_negative", nil)
	}
	if definition.Limits.MaxModelCalls < 0 {
		return newFieldError("limits.max_model_calls", "must_not_be_negative", nil)
	}
	if err = validateConfig("model.config", definition.Model.Config); err != nil {
		return err
	}
	if err = validateConfig("memory.config", definition.Memory.Config); err != nil {
		return err
	}
	if err = validateConfig("sandbox.config", definition.Sandbox.Config); err != nil {
		return err
	}
	return nil
}

type namedBinding interface {
	ToolBinding | MiddlewareBinding
}

func validateBindings[T namedBinding](field string, bindings []T) (err error) {
	seen := make(map[string]struct{}, len(bindings))
	for i, binding := range bindings {
		var name string
		var config Config
		switch value := any(binding).(type) {
		case ToolBinding:
			name, config = value.Name, value.Config
		case MiddlewareBinding:
			name, config = value.Name, value.Config
		}
		name = strings.TrimSpace(name)
		nameField := fmt.Sprintf("%s[%d].name", field, i)
		if name == "" {
			return newFieldError(nameField, "required", nil)
		}
		if _, ok := seen[name]; ok {
			return newFieldError(nameField, "duplicate", nil)
		}
		seen[name] = struct{}{}
		if err = validateConfig(fmt.Sprintf("%s[%d].config", field, i), config); err != nil {
			return err
		}
	}
	return nil
}

func validateConfig(field string, config Config) (err error) {
	if config == nil {
		return nil
	}
	if _, err = json.Marshal(config); err != nil {
		return newFieldError(field, "must_be_json_compatible", err)
	}
	return nil
}

func newFieldError(field string, code string, cause error) (err *FieldError) {
	err = &FieldError{Field: field, Code: code, Cause: cause}
	return err
}
