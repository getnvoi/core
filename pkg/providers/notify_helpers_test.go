package providers_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/providers"
)

func TestStringField_Happy(t *testing.T) {
	spec := providers.AlertSpec{
		Provider: "test",
		Fields:   map[string]interface{}{"k": "v"},
	}
	got, err := providers.StringField(spec, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v" {
		t.Errorf("got %q, want %q", got, "v")
	}
}

func TestStringField_Missing(t *testing.T) {
	spec := providers.AlertSpec{Provider: "test", Fields: map[string]interface{}{}}
	_, err := providers.StringField(spec, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "test: missing required") {
		t.Errorf("error message wrong: %v", err)
	}
}

func TestStringField_WrongType(t *testing.T) {
	spec := providers.AlertSpec{Provider: "test", Fields: map[string]interface{}{"k": 42}}
	_, err := providers.StringField(spec, "k")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("error message wrong: %v", err)
	}
}

func TestStringField_Empty(t *testing.T) {
	spec := providers.AlertSpec{Provider: "test", Fields: map[string]interface{}{"k": ""}}
	_, err := providers.StringField(spec, "k")
	if err == nil {
		t.Fatal("expected error for empty value")
	}
}

func TestStringListField_HappyInterfaceSlice(t *testing.T) {
	spec := providers.AlertSpec{
		Provider: "test",
		Fields:   map[string]interface{}{"to": []interface{}{"a", "b", "c"}},
	}
	got, err := providers.StringListField(spec, "to")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("got %v", got)
	}
}

func TestStringListField_HappyStringSlice(t *testing.T) {
	spec := providers.AlertSpec{
		Provider: "test",
		Fields:   map[string]interface{}{"to": []string{"a", "b"}},
	}
	got, err := providers.StringListField(spec, "to")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestStringListField_Missing(t *testing.T) {
	spec := providers.AlertSpec{Provider: "test", Fields: map[string]interface{}{}}
	_, err := providers.StringListField(spec, "to")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStringListField_WrongElementType(t *testing.T) {
	spec := providers.AlertSpec{
		Provider: "test",
		Fields:   map[string]interface{}{"to": []interface{}{"ok", 42}},
	}
	_, err := providers.StringListField(spec, "to")
	if err == nil {
		t.Fatal("expected error for non-string element")
	}
	if !strings.Contains(err.Error(), "to[1]") {
		t.Errorf("error should name the bad index: %v", err)
	}
}

func TestStringListField_EmptyList(t *testing.T) {
	spec := providers.AlertSpec{
		Provider: "test",
		Fields:   map[string]interface{}{"to": []interface{}{}},
	}
	_, err := providers.StringListField(spec, "to")
	if err == nil {
		t.Fatal("expected error for empty list")
	}
}
