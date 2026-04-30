package main

import (
	"reflect"
	"testing"
)

func TestBuildLogsArgs_DefaultsToBareTarget(t *testing.T) {
	got := buildLogsArgs("deployment/web", false, -1, "")
	want := []string{"logs", "deployment/web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildLogsArgs_FollowAddsFlag(t *testing.T) {
	got := buildLogsArgs("deployment/web", true, -1, "")
	want := []string{"logs", "deployment/web", "--follow"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildLogsArgs_TailAndSince(t *testing.T) {
	got := buildLogsArgs("statefulset/postgres", false, 100, "5m")
	want := []string{"logs", "statefulset/postgres", "--tail=100", "--since=5m"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildLogsArgs_NegativeTailIsAll(t *testing.T) {
	got := buildLogsArgs("deployment/web", false, -1, "")
	for _, a := range got {
		if a == "--tail" || len(a) >= 7 && a[:7] == "--tail=" {
			t.Errorf("--tail should be omitted for negative value, got %v", got)
		}
	}
}

func TestBuildLogsArgs_AllFlagsTogether(t *testing.T) {
	got := buildLogsArgs("deployment/api", true, 50, "1h")
	want := []string{"logs", "deployment/api", "--follow", "--tail=50", "--since=1h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
