package utils

import (
	"reflect"
	"testing"
)

func TestSortedKeys_Strings(t *testing.T) {
	got := SortedKeys(map[string]int{"zed": 1, "alpha": 2, "middle": 3})
	want := []string{"alpha", "middle", "zed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSortedKeys_Ints(t *testing.T) {
	got := SortedKeys(map[int]string{3: "c", 1: "a", 2: "b"})
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSortedKeys_Empty(t *testing.T) {
	got := SortedKeys(map[string]int{})
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}
