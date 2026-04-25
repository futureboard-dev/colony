package module

import (
	"testing"
)

func TestCommandsForTypescript(t *testing.T) {
	cmds, err := CommandsFor("typescript")
	if err != nil {
		t.Fatal(err)
	}
	if cmds.Format == "" || cmds.TypeCheck == "" || cmds.Test == "" {
		t.Errorf("missing commands: %+v", cmds)
	}
	// alias should resolve identically
	cmdsAlias, _ := CommandsFor("ts")
	if cmds != cmdsAlias {
		t.Error("ts and typescript should produce identical commands")
	}
}

func TestCommandsForPython(t *testing.T) {
	cmds, err := CommandsFor("python")
	if err != nil {
		t.Fatal(err)
	}
	if cmds.Format == "" || cmds.TypeCheck == "" || cmds.Test == "" {
		t.Errorf("missing commands: %+v", cmds)
	}
	cmdsAlias, _ := CommandsFor("py")
	if cmds != cmdsAlias {
		t.Error("py and python should produce identical commands")
	}
}

func TestCommandsForGo(t *testing.T) {
	cmds, err := CommandsFor("go")
	if err != nil {
		t.Fatal(err)
	}
	if cmds.Format == "" || cmds.TypeCheck == "" || cmds.Test == "" {
		t.Errorf("missing commands: %+v", cmds)
	}
}

func TestCommandsForCaseInsensitive(t *testing.T) {
	_, err := CommandsFor("Go")
	if err != nil {
		t.Error("CommandsFor should be case-insensitive")
	}
	_, err = CommandsFor("TypeScript")
	if err != nil {
		t.Error("CommandsFor should be case-insensitive")
	}
}

func TestCommandsForUnknown(t *testing.T) {
	_, err := CommandsFor("ruby")
	if err == nil {
		t.Error("expected error for unknown language")
	}
}
