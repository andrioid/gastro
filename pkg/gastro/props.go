package gastro

import (
	"fmt"
	"reflect"
	"strconv"
)

// MapToStruct converts a map[string]any (from template dict calls) into a
// typed struct T. Handles type coercion for common cases (string->bool,
// string->int, float64->int).
func MapToStruct[T any](m map[string]any) (T, error) {
	var result T
	rv := reflect.ValueOf(&result).Elem()
	rt := rv.Type()

	if rt.Kind() != reflect.Struct {
		return result, fmt.Errorf("MapToStruct: T must be a struct, got %s", rt.Kind())
	}

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fieldVal := rv.Field(i)

		val, ok := m[field.Name]
		if !ok {
			continue
		}

		if err := setField(fieldVal, val, field.Name); err != nil {
			return result, err
		}
	}

	return result, nil
}

func setField(fieldVal reflect.Value, val any, fieldName string) error {
	if val == nil {
		return nil
	}

	valReflect := reflect.ValueOf(val)

	// Direct assignment if types match
	if valReflect.Type().AssignableTo(fieldVal.Type()) {
		fieldVal.Set(valReflect)
		return nil
	}

	// Type coercion
	switch fieldVal.Kind() {
	case reflect.String:
		return setStringField(fieldVal, val, fieldName)
	case reflect.Bool:
		return setBoolField(fieldVal, val, fieldName)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return setIntField(fieldVal, val, fieldName)
	case reflect.Float32, reflect.Float64:
		return setFloatField(fieldVal, val, fieldName)
	default:
		return fmt.Errorf("prop %q: cannot assign %T to %s", fieldName, val, fieldVal.Type())
	}
}

func setStringField(fieldVal reflect.Value, val any, fieldName string) error {
	switch v := val.(type) {
	case string:
		fieldVal.SetString(v)
		return nil
	default:
		fieldVal.SetString(fmt.Sprintf("%v", v))
		return nil
	}
}

func setBoolField(fieldVal reflect.Value, val any, fieldName string) error {
	switch v := val.(type) {
	case bool:
		fieldVal.SetBool(v)
		return nil
	case string:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("prop %q: cannot convert %q to bool: %w", fieldName, v, err)
		}
		fieldVal.SetBool(b)
		return nil
	default:
		return fmt.Errorf("prop %q: cannot convert %T to bool", fieldName, val)
	}
}

func setIntField(fieldVal reflect.Value, val any, fieldName string) error {
	switch v := val.(type) {
	case int:
		fieldVal.SetInt(int64(v))
		return nil
	case int64:
		fieldVal.SetInt(v)
		return nil
	case float64:
		fieldVal.SetInt(int64(v))
		return nil
	case float32:
		fieldVal.SetInt(int64(v))
		return nil
	case string:
		i, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("prop %q: cannot convert %q to int: %w", fieldName, v, err)
		}
		fieldVal.SetInt(i)
		return nil
	default:
		return fmt.Errorf("prop %q: cannot convert %T to int", fieldName, val)
	}
}

func setFloatField(fieldVal reflect.Value, val any, fieldName string) error {
	switch v := val.(type) {
	case float64:
		fieldVal.SetFloat(v)
		return nil
	case float32:
		fieldVal.SetFloat(float64(v))
		return nil
	case int:
		fieldVal.SetFloat(float64(v))
		return nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("prop %q: cannot convert %q to float: %w", fieldName, v, err)
		}
		fieldVal.SetFloat(f)
		return nil
	default:
		return fmt.Errorf("prop %q: cannot convert %T to float", fieldName, val)
	}
}
