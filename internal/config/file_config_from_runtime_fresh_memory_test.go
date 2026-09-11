package config

import (
	"reflect"
	"testing"
)

func scribbleReferences(v reflect.Value) int {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return 0
		}
		return 1 + scribbleValue(v.Elem())
	case reflect.Slice:
		n := 0
		for i := 0; i < v.Len(); i++ {
			n += 1 + scribbleValue(v.Index(i))
		}
		return n
	case reflect.Map:
		n := 0
		for _, key := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(key))
			n += 1 + scribbleValue(elem)
			v.SetMapIndex(key, elem)
		}
		return n
	case reflect.Struct:
		n := 0
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				n += scribbleReferences(v.Field(i))
			}
		}
		return n
	}
	return 0
}

func scribbleValue(v reflect.Value) int {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.String:
		v.SetString(v.String() + "-mutated")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 1)
	default:
		return scribbleReferences(v)
	}
	return 0
}

func TestWritingThroughEveryFileConfigReferenceLeavesTheRuntimeUnchanged(t *testing.T) {
	cfg := populatedRuntimeConfig(t)
	cfg.CookieSecure = ptr(true)
	want := populatedRuntimeConfig(t)
	want.CookieSecure = ptr(true)
	if !reflect.DeepEqual(cfg, want) {
		t.Fatal("populatedRuntimeConfig is not deterministic, so it cannot serve as the control")
	}

	fc := FileConfigFromRuntime(cfg)
	if written := scribbleReferences(reflect.ValueOf(&fc).Elem()); written < 80 {
		t.Fatalf("wrote through only %d references; the fixture no longer populates the reference-typed fields", written)
	}

	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("a write through the returned FileConfig reached the runtime:\n got %+v\nwant %+v", *cfg, *want)
	}
}
