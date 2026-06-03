package util

import (
	"encoding/json"
	"maps"
)

// UnmarshalWithRest parses known keys into `known` and returns the remainder.
func UnmarshalWithRest(data []byte, known map[string]any) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for key, dest := range known {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, dest); err != nil {
				return nil, err
			}
			delete(raw, key)
		}
	}
	return raw, nil
}

// MarshalWithRest merges known keys with the leftover raw fields and marshals.
func MarshalWithRest(known map[string]any, rest map[string]json.RawMessage) ([]byte, error) {
	out := make(map[string]json.RawMessage, len(rest)+len(known))
	maps.Copy(out, rest)
	for key, src := range known {
		b, err := json.Marshal(src)
		if err != nil {
			return nil, err
		}
		out[key] = b
	}
	return json.Marshal(out)
}
