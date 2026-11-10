package cell

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// FieldType is the type of a configuration field.
type FieldType string

const (
	FieldString FieldType = "string"
	FieldInt    FieldType = "int"
	FieldBool   FieldType = "bool"
)

// ConfigField documents one configuration option of an architecture.
type ConfigField struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Required    bool      `json:"required,omitempty"`
	Default     any       `json:"default,omitempty"`
	Min         *int64    `json:"min,omitempty"`
	Max         *int64    `json:"max,omitempty"`
	Description string    `json:"description,omitempty"`
}

// ConfigError is a configuration problem for one field ("" = whole config).
type ConfigError struct {
	Field   string
	Message string
}

// Int64 returns a pointer to v (for ConfigField.Min/Max literals).
func Int64(v int64) *int64 { return &v }

// ValidateConfig checks cfg against schema: unknown fields, required
// fields, types, and integer ranges. Errors are returned in field order.
func ValidateConfig(schema []ConfigField, cfg map[string]any) []ConfigError {
	var errs []ConfigError
	known := map[string]ConfigField{}
	for _, f := range schema {
		known[f.Name] = f
	}
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := known[k]; !ok {
			errs = append(errs, ConfigError{k, "unknown configuration field"})
		}
	}
	for _, f := range schema {
		v, ok := cfg[f.Name]
		if !ok || v == nil {
			if f.Required {
				errs = append(errs, ConfigError{f.Name, "required configuration field is missing"})
			}
			continue
		}
		switch f.Type {
		case FieldString:
			if s, ok := v.(string); !ok || s == "" {
				errs = append(errs, ConfigError{f.Name, "must be a non-empty string"})
			}
		case FieldBool:
			if _, ok := v.(bool); !ok {
				errs = append(errs, ConfigError{f.Name, "must be a boolean"})
			}
		case FieldInt:
			n, ok := ToInt64(v)
			if !ok {
				errs = append(errs, ConfigError{f.Name, "must be an integer"})
				continue
			}
			if f.Min != nil && n < *f.Min {
				errs = append(errs, ConfigError{f.Name, fmt.Sprintf("must be >= %d", *f.Min)})
			}
			if f.Max != nil && n > *f.Max {
				errs = append(errs, ConfigError{f.Name, fmt.Sprintf("must be <= %d", *f.Max)})
			}
		}
	}
	return errs
}

// ToInt64 converts YAML/JSON numeric values to int64.
func ToInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case float64:
		if n != math.Trunc(n) || n < math.MinInt64 || n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// ConfigInt reads an integer field, falling back to def.
func ConfigInt(cfg map[string]any, name string, def int64) int64 {
	if v, ok := cfg[name]; ok {
		if n, ok := ToInt64(v); ok {
			return n
		}
	}
	return def
}

// ConfigString reads a string field, falling back to def.
func ConfigString(cfg map[string]any, name, def string) string {
	if v, ok := cfg[name].(string); ok {
		return v
	}
	return def
}
