//go:build !windows

package thread

import (
	"encoding/json"
	"fmt"
	"strings"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"github.com/cloudwego/eino/schema"
)

const cloudInputPartsExtraKey = "__cloudagent_input_parts__"

type cloudInputParts []protoinput.MessagePart

func cloudUserMessageToSchemaMessage(input protoinput.UserMessage) (*schema.Message, error) {
	originalParts := normalizeCloudInputParts(input.Parts)
	parts := make([]schema.MessageInputPart, 0, len(originalParts))
	text := make([]string, 0, len(originalParts))
	hasNonTextPart := false
	for i, part := range originalParts {
		einoPart, err := cloudPartToSchemaInputPart(part)
		if err != nil {
			return nil, fmt.Errorf("parts[%d]: %w", i, err)
		}
		parts = append(parts, einoPart)
		if part.Type == protoinput.MessagePartTypeText {
			text = append(text, part.Text)
		} else {
			hasNonTextPart = true
		}
	}

	msg := &schema.Message{Role: schema.User, Extra: cloudExtraToSchemaExtra(input.Extra)}
	if hasNonTextPart {
		msg.UserInputMultiContent = parts
	} else {
		msg.Content = strings.Join(text, "\n")
	}
	attachOriginalCloudInputParts(msg, originalParts)
	return msg, nil
}

func cloudPartToSchemaInputPart(part protoinput.MessagePart) (schema.MessageInputPart, error) {
	switch part.Type {
	case protoinput.MessagePartTypeText:
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeText,
			Text:  strings.TrimSpace(part.Text),
			Extra: cloudExtraToSchemaExtra(part.Extra),
		}, nil
	case protoinput.MessagePartTypeImage:
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{MessagePartCommon: schemaMessagePartCommon(part), Detail: schema.ImageURLDetail(strings.TrimSpace(part.Detail))},
			Extra: cloudExtraToSchemaExtra(part.Extra),
		}, nil
	case protoinput.MessagePartTypeAudio:
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeAudioURL,
			Audio: &schema.MessageInputAudio{MessagePartCommon: schemaMessagePartCommon(part)},
			Extra: cloudExtraToSchemaExtra(part.Extra),
		}, nil
	case protoinput.MessagePartTypeVideo:
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeVideoURL,
			Video: &schema.MessageInputVideo{MessagePartCommon: schemaMessagePartCommon(part)},
			Extra: cloudExtraToSchemaExtra(part.Extra),
		}, nil
	case protoinput.MessagePartTypeFile:
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeFileURL,
			File:  &schema.MessageInputFile{MessagePartCommon: schemaMessagePartCommon(part), Name: strings.TrimSpace(part.Name)},
			Extra: cloudExtraToSchemaExtra(part.Extra),
		}, nil
	default:
		return schema.MessageInputPart{}, fmt.Errorf("unsupported part type: %s", part.Type)
	}
}

func schemaUserMessageToCloudParts(message *schema.Message) []protoevent.MessagePart {
	if parts := originalCloudInputParts(message); len(parts) > 0 {
		return inputPartsForEvent(parts)
	}
	if message == nil {
		return nil
	}
	if message.Content != "" {
		return textParts(message.Content)
	}
	if len(message.UserInputMultiContent) == 0 {
		return nil
	}
	parts := make([]protoevent.MessagePart, 0, len(message.UserInputMultiContent))
	for _, part := range message.UserInputMultiContent {
		converted, ok := schemaInputPartToCloudPart(part)
		if ok {
			parts = append(parts, converted)
		}
	}
	return parts
}

func schemaInputPartToCloudPart(part schema.MessageInputPart) (protoevent.MessagePart, bool) {
	switch part.Type {
	case schema.ChatMessagePartTypeText:
		text := strings.TrimSpace(part.Text)
		if text == "" {
			return protoevent.MessagePart{}, false
		}
		return protoevent.MessagePart{Type: protoevent.MessagePartTypeText, Text: text, Extra: schemaExtraToCloudExtra(part.Extra)}, true
	case schema.ChatMessagePartTypeImageURL:
		out := cloudPartFromCommon(protoevent.MessagePartTypeImage, messageInputImageCommon(part.Image))
		out.Extra = mergeCloudExtra(schemaExtraToCloudExtra(part.Extra), out.Extra)
		if part.Image != nil {
			out.Detail = string(part.Image.Detail)
		}
		return out, true
	case schema.ChatMessagePartTypeAudioURL:
		out := cloudPartFromCommon(protoevent.MessagePartTypeAudio, messageInputAudioCommon(part.Audio))
		out.Extra = mergeCloudExtra(schemaExtraToCloudExtra(part.Extra), out.Extra)
		return out, true
	case schema.ChatMessagePartTypeVideoURL:
		out := cloudPartFromCommon(protoevent.MessagePartTypeVideo, messageInputVideoCommon(part.Video))
		out.Extra = mergeCloudExtra(schemaExtraToCloudExtra(part.Extra), out.Extra)
		return out, true
	case schema.ChatMessagePartTypeFileURL:
		out := cloudPartFromCommon(protoevent.MessagePartTypeFile, messageInputFileCommon(part.File))
		out.Extra = mergeCloudExtra(schemaExtraToCloudExtra(part.Extra), out.Extra)
		if part.File != nil {
			out.Name = part.File.Name
		}
		return out, true
	default:
		return protoevent.MessagePart{}, false
	}
}

func schemaAssistantMessageToCloudParts(message *schema.Message) []protoevent.MessagePart {
	if message == nil {
		return nil
	}
	if len(message.AssistantGenMultiContent) > 0 {
		parts := make([]protoevent.MessagePart, 0, len(message.AssistantGenMultiContent))
		for _, part := range message.AssistantGenMultiContent {
			converted, ok := schemaOutputPartToCloudPart(part)
			if ok {
				parts = append(parts, converted)
			}
		}
		if len(parts) > 0 {
			return parts
		}
	}
	return textParts(message.Content)
}

func schemaOutputPartToCloudPart(part schema.MessageOutputPart) (protoevent.MessagePart, bool) {
	switch part.Type {
	case schema.ChatMessagePartTypeText:
		return protoevent.MessagePart{Type: protoevent.MessagePartTypeText, Text: part.Text, Extra: schemaExtraToCloudExtra(part.Extra)}, true
	case schema.ChatMessagePartTypeImageURL:
		out := cloudPartFromCommon(protoevent.MessagePartTypeImage, messageOutputImageCommon(part.Image))
		out.Extra = mergeCloudExtra(schemaExtraToCloudExtra(part.Extra), out.Extra)
		return out, true
	case schema.ChatMessagePartTypeAudioURL:
		out := cloudPartFromCommon(protoevent.MessagePartTypeAudio, messageOutputAudioCommon(part.Audio))
		out.Extra = mergeCloudExtra(schemaExtraToCloudExtra(part.Extra), out.Extra)
		return out, true
	case schema.ChatMessagePartTypeVideoURL:
		out := cloudPartFromCommon(protoevent.MessagePartTypeVideo, messageOutputVideoCommon(part.Video))
		out.Extra = mergeCloudExtra(schemaExtraToCloudExtra(part.Extra), out.Extra)
		return out, true
	default:
		return protoevent.MessagePart{Type: protoevent.MessagePartType(strings.TrimSuffix(string(part.Type), "_url")), Extra: schemaExtraToCloudExtra(part.Extra)}, true
	}
}

func attachOriginalCloudInputParts(msg *schema.Message, parts []protoinput.MessagePart) {
	if msg == nil || len(parts) == 0 {
		return
	}
	if msg.Extra == nil {
		msg.Extra = make(map[string]any, 1)
	}
	msg.Extra[cloudInputPartsExtraKey] = cloudInputParts(cloneCloudInputParts(parts))
}

func originalCloudInputParts(msg *schema.Message) []protoinput.MessagePart {
	if msg == nil || msg.Extra == nil {
		return nil
	}
	switch parts := msg.Extra[cloudInputPartsExtraKey].(type) {
	case cloudInputParts:
		return cloneCloudInputParts([]protoinput.MessagePart(parts))
	case *cloudInputParts:
		if parts == nil {
			return nil
		}
		return cloneCloudInputParts([]protoinput.MessagePart(*parts))
	case []protoinput.MessagePart:
		return cloneCloudInputParts(parts)
	default:
		return nil
	}
}

func inputPartsForEvent(parts []protoinput.MessagePart) []protoevent.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]protoevent.MessagePart, 0, len(parts))
	for _, part := range parts {
		out = append(out, protoevent.MessagePart(cloneCloudInputPart(part)))
	}
	return out
}

func normalizeCloudInputParts(parts []protoinput.MessagePart) []protoinput.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]protoinput.MessagePart, len(parts))
	for i, part := range parts {
		out[i] = protoinput.MessagePart{
			Type:       part.Type,
			Text:       strings.TrimSpace(part.Text),
			URL:        strings.TrimSpace(part.URL),
			Base64Data: strings.TrimSpace(part.Base64Data),
			MIMEType:   strings.TrimSpace(part.MIMEType),
			Name:       strings.TrimSpace(part.Name),
			Detail:     strings.TrimSpace(part.Detail),
			Extra:      cloneCloudExtra(part.Extra),
		}
	}
	return out
}

func cloneCloudInputParts(parts []protoinput.MessagePart) []protoinput.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]protoinput.MessagePart, len(parts))
	for i, part := range parts {
		out[i] = cloneCloudInputPart(part)
	}
	return out
}

func schemaMessagePartCommon(part protoinput.MessagePart) schema.MessagePartCommon {
	common := schema.MessagePartCommon{
		MIMEType: strings.TrimSpace(part.MIMEType),
		Extra:    cloudExtraToSchemaExtra(part.Extra),
	}
	if url := strings.TrimSpace(part.URL); url != "" {
		common.URL = &url
	}
	if data := strings.TrimSpace(part.Base64Data); data != "" {
		common.Base64Data = &data
	}
	return common
}

func cloudPartFromCommon(partType protoevent.MessagePartType, common schema.MessagePartCommon) protoevent.MessagePart {
	out := protoevent.MessagePart{Type: partType, MIMEType: common.MIMEType, Extra: schemaExtraToCloudExtra(common.Extra)}
	if common.URL != nil {
		out.URL = *common.URL
	}
	if common.Base64Data != nil {
		out.Base64Data = *common.Base64Data
	}
	return out
}

func cloneCloudInputPart(part protoinput.MessagePart) protoinput.MessagePart {
	return protoinput.MessagePart{
		Type:       part.Type,
		Text:       part.Text,
		URL:        part.URL,
		Base64Data: part.Base64Data,
		MIMEType:   part.MIMEType,
		Name:       part.Name,
		Detail:     part.Detail,
		Extra:      cloneCloudExtra(part.Extra),
	}
}

func cloneCloudExtra(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = cloneRawMessage(v)
	}
	return out
}

func cloudExtraToSchemaExtra(in map[string]json.RawMessage) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneRawMessage(v)
	}
	return out
}

func schemaExtraToCloudExtra(in map[string]any) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		raw, ok := rawMessageFromAny(v)
		if ok {
			out[k] = raw
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func rawMessageFromAny(v any) (json.RawMessage, bool) {
	switch raw := v.(type) {
	case json.RawMessage:
		return cloneRawMessage(raw), true
	case *json.RawMessage:
		if raw == nil {
			return nil, false
		}
		return cloneRawMessage(*raw), true
	case []byte:
		if !json.Valid(raw) {
			break
		}
		return cloneRawMessage(json.RawMessage(raw)), true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(b), true
}

func mergeCloudExtra(base map[string]json.RawMessage, overrides map[string]json.RawMessage) map[string]json.RawMessage {
	if len(base) == 0 {
		return cloneCloudExtra(overrides)
	}
	out := cloneCloudExtra(base)
	for k, v := range overrides {
		out[k] = cloneRawMessage(v)
	}
	return out
}

func cloneRawMessage(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}
	return append(json.RawMessage(nil), in...)
}

func messageInputImageCommon(part *schema.MessageInputImage) schema.MessagePartCommon {
	if part == nil {
		return schema.MessagePartCommon{}
	}
	return part.MessagePartCommon
}

func messageInputAudioCommon(part *schema.MessageInputAudio) schema.MessagePartCommon {
	if part == nil {
		return schema.MessagePartCommon{}
	}
	return part.MessagePartCommon
}

func messageInputVideoCommon(part *schema.MessageInputVideo) schema.MessagePartCommon {
	if part == nil {
		return schema.MessagePartCommon{}
	}
	return part.MessagePartCommon
}

func messageInputFileCommon(part *schema.MessageInputFile) schema.MessagePartCommon {
	if part == nil {
		return schema.MessagePartCommon{}
	}
	return part.MessagePartCommon
}

func messageOutputImageCommon(part *schema.MessageOutputImage) schema.MessagePartCommon {
	if part == nil {
		return schema.MessagePartCommon{}
	}
	return part.MessagePartCommon
}

func messageOutputAudioCommon(part *schema.MessageOutputAudio) schema.MessagePartCommon {
	if part == nil {
		return schema.MessagePartCommon{}
	}
	return part.MessagePartCommon
}

func messageOutputVideoCommon(part *schema.MessageOutputVideo) schema.MessagePartCommon {
	if part == nil {
		return schema.MessagePartCommon{}
	}
	return part.MessagePartCommon
}
