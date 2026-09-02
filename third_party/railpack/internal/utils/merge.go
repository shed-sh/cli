package utils

import (
	"reflect"
)

// MergeStructs merges multiple structs of the same type, with later values taking precedence.
// Only non-zero values from later structs will override earlier values.
func MergeStructs(dst any, srcs ...any) {
	for _, src := range srcs {
		if src == nil {
			continue
		}
		mergeStruct(dst, src)
	}
}

// mergeStruct merges two structs of the same type, taking non-zero values from src
func mergeStruct(dst, src any) {
	dstValue := reflect.ValueOf(dst).Elem()
	srcValue := reflect.ValueOf(src)

	if srcValue.Kind() == reflect.Pointer {
		srcValue = srcValue.Elem()
	}

	// If it's not a struct, we can't iterate over fields
	if srcValue.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < srcValue.NumField(); i++ {
		srcField := srcValue.Field(i)
		dstField := dstValue.Field(i)

		switch srcField.Kind() {
		case reflect.Map:
			mergeMap(dstField, srcField)

		case reflect.Slice:
			// This is specific behaviour for how we want to merge slices
			// Always override with the non-nil slice
			if !srcField.IsNil() {
				dstField.Set(srcField)
			}

		case reflect.Pointer:
			mergePtr(dstField, srcField)

		case reflect.Struct:
			mergeStruct(dstField.Addr().Interface(), srcField.Interface())

		default:
			if !isZeroValue(srcField) {
				dstField.Set(srcField)
			}
		}
	}
}

func mergeMap(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}

	if dst.IsNil() {
		dst.Set(reflect.MakeMap(src.Type()))
	}

	for _, key := range src.MapKeys() {
		srcValue := src.MapIndex(key)
		dstValue := dst.MapIndex(key)

		if srcValue.Kind() == reflect.Pointer && srcValue.Elem().Kind() == reflect.Struct {
			if !dstValue.IsValid() {
				dst.SetMapIndex(key, srcValue)
			} else {
				mergeStruct(dstValue.Interface(), srcValue.Interface())
			}
			continue
		}

		dst.SetMapIndex(key, srcValue)
	}
}

func mergePtr(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}

	switch src.Elem().Kind() {
	case reflect.Struct:
		if dst.IsNil() {
			dst.Set(reflect.New(src.Elem().Type()))
		}
		mergeStruct(dst.Interface(), src.Interface())

	case reflect.Slice:
		if dst.IsNil() || !src.Elem().IsNil() {
			dst.Set(src)
		}

	default:
		dst.Set(src)
	}
}

// isZeroValue checks if a reflect.Value is its zero value
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	case reflect.Struct:
		// For structs, check if all fields are zero values
		for _, field := range v.Fields() {
			if !isZeroValue(field) {
				return false
			}
		}
		return true
	}
	return false
}
