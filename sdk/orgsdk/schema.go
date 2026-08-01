package orgsdk

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

func schemaFor[T any]() json.RawMessage {
	typeOf := reflect.TypeOf((*T)(nil)).Elem()
	schema := schemaForType(typeOf, map[reflect.Type]bool{})
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	return encoded
}

func schemaForType(value reflect.Type, visiting map[reflect.Type]bool) any {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if value == reflect.TypeOf(json.RawMessage{}) || value.Kind() == reflect.Interface {
		return map[string]any{}
	}
	switch value.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaForType(value.Elem(), visiting)}
	case reflect.Map:
		if value.Key().Kind() != reflect.String {
			return map[string]any{}
		}
		return map[string]any{"type": "object", "additionalProperties": schemaForType(value.Elem(), visiting)}
	case reflect.Struct:
		if visiting[value] {
			return map[string]any{"type": "object"}
		}
		visiting[value] = true
		defer delete(visiting, value)
		properties := map[string]any{}
		required := make([]string, 0, value.NumField())
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if !field.IsExported() {
				continue
			}
			name, optional, skip := jsonField(field)
			if skip {
				continue
			}
			properties[name] = schemaForType(field.Type, visiting)
			if !optional {
				required = append(required, name)
			}
		}
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	default:
		return map[string]any{}
	}
}

func jsonField(field reflect.StructField) (name string, optional, skip bool) {
	tag := field.Tag.Get("json")
	parts := strings.Split(tag, ",")
	if len(parts) > 0 && parts[0] == "-" {
		return "", false, true
	}
	name = field.Name
	if len(parts) > 0 && parts[0] != "" {
		name = parts[0]
	}
	for _, option := range parts[1:] {
		if option == "omitempty" || option == "omitzero" {
			optional = true
		}
	}
	return name, optional, false
}
