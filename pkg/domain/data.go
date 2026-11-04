package domain

import "encoding/json"

// DataMap returns data as a JSON object map (nil when data is empty or not an object), for wire encoding.
// Maps pass through untouched; any other value goes through a JSON round trip.
func DataMap(data any) map[string]any {
	switch v := data.(type) {
	case nil:
		return nil
	case map[string]any:
		return v
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}
