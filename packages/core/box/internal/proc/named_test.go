package proc

import "testing"

func TestANameFindsTheLiveCommandThatHasIt(t *testing.T) {
	tbl := New(t.TempDir())

	c, err := tbl.Spawn(Spec{Argv: []string{"sleep", "30"}, Name: "turn-abc"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c.Kill(); waitExited(t, c) }()

	found, ok := tbl.Named("turn-abc")
	if !ok || found.ID != c.ID {
		t.Fatalf("Named(turn-abc) = %v, %v; want %s", found, ok, c.ID)
	}
}

func TestANAMEISREUSEDSOAFINISHEDCOMMANDDOESNOTANSWER(t *testing.T) {
	// A session's second turn takes the same name. A stop that matched the
	// first would report success over a process still running.
	tbl := New(t.TempDir())

	done, err := tbl.Spawn(Spec{Argv: []string{"true"}, Name: "turn-abc"})
	if err != nil {
		t.Fatal(err)
	}
	waitExited(t, done)

	if _, ok := tbl.Named("turn-abc"); ok {
		t.Fatal("a finished command answered to its name")
	}

	live, err := tbl.Spawn(Spec{Argv: []string{"sleep", "30"}, Name: "turn-abc"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { live.Kill(); waitExited(t, live) }()

	found, ok := tbl.Named("turn-abc")
	if !ok || found.ID != live.ID {
		t.Fatal("the live command must be the one a name finds")
	}
}

func TestANamelessLookupFindsNothing(t *testing.T) {
	tbl := New(t.TempDir())

	if _, ok := tbl.Named(""); ok {
		t.Fatal("an empty name must not match a command with no name")
	}
}
