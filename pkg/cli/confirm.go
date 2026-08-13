package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
)

// confirm asks the y/n question a destructive command is gated on and reports
// whether it may proceed. A declined prompt prints "Aborted." and returns
// false, so a caller only has to return nil.
//
// With no terminal to ask on there is no question to put: huh opens /dev/tty
// itself and fails with "could not open a new TTY", which reads like a broken
// binary rather than a forgotten flag, and it fires on every confirm-gated
// command, so scripting any of them means finding out this way
// (fogpipe/cloud-workspace#22). The caller is told the flag instead. Reading
// the answer off a pipe is deliberately not the fallback — a redirect that was
// never meant as an answer would then delete something.
func confirm(title, description, affirmative string) (bool, error) {
	return confirmOn(os.Stdin, title, description, affirmative)
}

// requirePrompt refuses a command that is about to ask for a value it was not
// given, when there is no terminal to ask on. Same failure as confirm above —
// huh opens /dev/tty and reports that it could not — on the prompts that fill in
// a missing argument rather than gate a destructive one, so `project create`
// under CI names the argument instead (fogpipe/cloud-workspace#26).
func requirePrompt(instruction string) error {
	if isTerminal(os.Stdin) {
		return nil
	}
	return fmt.Errorf("no terminal to prompt on: %s", instruction)
}

func confirmOn(in *os.File, title, description, affirmative string) (bool, error) {
	if !isTerminal(in) {
		return false, errors.New("no terminal to confirm on: pass --yes to proceed without the prompt")
	}
	var ok bool
	if err := huh.NewConfirm().
		Title(title).
		Description(description).
		Affirmative(affirmative).
		Negative("Cancel").
		Value(&ok).
		Run(); err != nil {
		return false, err
	}
	if !ok {
		fmt.Println(mutedStyle.Render("Aborted."))
	}
	return ok, nil
}
