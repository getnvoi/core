package providers

import "fmt"

// Helpers for vendor packages extracting typed values out of
// AlertSpec.Fields (which is map[string]interface{} because yaml.v3
// decodes heterogeneous shapes that way). Shared here so every vendor
// uses the same accessor + the same error messages.

// StringField returns the named field as a non-empty string. Returns
// a clear error mentioning the provider name and field key when
// missing / wrong-typed / empty.
func StringField(spec AlertSpec, key string) (string, error) {
	raw, ok := spec.Fields[key]
	if !ok {
		return "", fmt.Errorf("%s: %s required", spec.Provider, key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s: %s must be a string (got %T)", spec.Provider, key, raw)
	}
	if s == "" {
		return "", fmt.Errorf("%s: %s required (empty value)", spec.Provider, key)
	}
	return s, nil
}

// StringListField returns the named field as a non-empty slice of
// non-empty strings. Accepts []interface{} (yaml.v3 default for inline
// lists) or []string (rare; included for completeness).
func StringListField(spec AlertSpec, key string) ([]string, error) {
	raw, ok := spec.Fields[key]
	if !ok {
		return nil, fmt.Errorf("%s: %s required", spec.Provider, key)
	}
	switch v := raw.(type) {
	case []string:
		if len(v) == 0 {
			return nil, fmt.Errorf("%s: %s must contain at least one entry", spec.Provider, key)
		}
		return v, nil
	case []interface{}:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s: %s[%d] must be a string (got %T)", spec.Provider, key, i, item)
			}
			if s == "" {
				return nil, fmt.Errorf("%s: %s[%d] must be non-empty", spec.Provider, key, i)
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s: %s must contain at least one entry", spec.Provider, key)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: %s must be a list of strings (got %T)", spec.Provider, key, raw)
	}
}
