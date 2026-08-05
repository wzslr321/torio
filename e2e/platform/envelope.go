package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type envelope struct {
	SchemaVersion string          `json:"schema_version"`
	OK            bool            `json:"ok"`
	Command       string          `json:"command"`
	Data          map[string]any  `json:"data"`
	Warnings      json.RawMessage `json:"warnings"`
	Error         json.RawMessage `json:"error"`
}

func decodeEnvelope(body []byte, command string) (envelope, error) {
	if err := rejectDuplicateObjectKeys(body); err != nil {
		return envelope{}, fmt.Errorf("validate JSON envelope keys: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var got envelope
	if err := dec.Decode(&got); err != nil {
		return envelope{}, fmt.Errorf("decode JSON envelope: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return envelope{}, fmt.Errorf("expected one JSON document, trailing decode: %v", err)
	}
	if got.SchemaVersion != "1" || !got.OK || got.Command != command || got.Data == nil {
		return envelope{}, fmt.Errorf("unexpected success envelope: schema=%q ok=%v command=%q", got.SchemaVersion, got.OK, got.Command)
	}
	if len(got.Warnings) == 0 || len(got.Error) == 0 {
		return envelope{}, errors.New("success envelope is missing warnings or error")
	}
	var warnings []any
	if err := json.Unmarshal(got.Warnings, &warnings); err != nil {
		return envelope{}, fmt.Errorf("decode warnings: %w", err)
	}
	if len(warnings) != 0 {
		return envelope{}, fmt.Errorf("success envelope contains warnings: %s", got.Warnings)
	}
	if string(got.Error) != "null" {
		return envelope{}, fmt.Errorf("success envelope contains error: %s", got.Error)
	}
	return got, nil
}

func rejectDuplicateObjectKeys(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := scanJSONValue(dec, "$"); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("expected one JSON document, trailing token: %v", err)
	}
	return nil
}

func scanJSONValue(dec *json.Decoder, path string) error {
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, keyErr := dec.Token()
			if keyErr != nil {
				return fmt.Errorf("read object key at %s: %w", path, keyErr)
			}
			key, keyOK := keyToken.(string)
			if !keyOK {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(dec, path+"."+key); err != nil {
				return err
			}
		}
		end, endErr := dec.Token()
		if endErr != nil {
			return fmt.Errorf("close object at %s: %w", path, endErr)
		}
		if end != json.Delim('}') {
			return fmt.Errorf("unexpected object terminator %v at %s", end, path)
		}
	case '[':
		index := 0
		for dec.More() {
			if err := scanJSONValue(dec, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		end, endErr := dec.Token()
		if endErr != nil {
			return fmt.Errorf("close array at %s: %w", path, endErr)
		}
		if end != json.Delim(']') {
			return fmt.Errorf("unexpected array terminator %v at %s", end, path)
		}
	default:
		return fmt.Errorf("unexpected delimiter %q at %s", delim, path)
	}
	return nil
}

func nestedValue(value any, path []string) (any, bool) {
	current := value
	for _, part := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
