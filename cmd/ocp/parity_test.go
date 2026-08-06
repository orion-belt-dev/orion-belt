package main

import (
	"testing"

	"github.com/zrougamed/orion-belt/pkg/cliparity"
)

func TestCoveredCommandsExist(t *testing.T) {
	for _, err := range cliparity.VerifyCommands(rootCmd, "ocp") {
		t.Errorf("%v", err)
	}
}

func TestHelpTextIsComplete(t *testing.T) {
	for _, err := range cliparity.VerifyHelp(rootCmd) {
		t.Errorf("%v", err)
	}
}
