//go:build nogui

package cli

import "errors"

const hasGUI = false

func runGUI(bool, string) error { return errors.New("this build has no GUI; run `magpie-friday tui`") }

func runPanel() error { return runGUI(false, "") }

func runWindow(string) error { return runGUI(true, "") }
