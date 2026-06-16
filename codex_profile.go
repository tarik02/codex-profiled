package main

import (
	"os"
	"path/filepath"
	"strings"
)

func codexOverlayConfigPath(codexHome, profileName string) string {
	return filepath.Join(codexHome, profileName+".config.toml")
}

func hasCodexProfileOverlay(codexHome, profileName string) bool {
	_, err := os.Stat(codexOverlayConfigPath(codexHome, profileName))
	return err == nil
}

func argsHasCodexProfile(args []string) bool {
	for i, arg := range args {
		if strings.HasPrefix(arg, "--profile=") {
			return true
		}
		if arg != "--profile" && arg != "-p" {
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			return true
		}
	}
	return false
}

func codexProfileSupported(args []string) bool {
	if len(args) == 0 {
		return true
	}

	command := args[0]
	if strings.HasPrefix(command, "-") {
		return true
	}

	switch command {
	case "exec", "review", "resume", "fork", "sandbox", "mcp":
		return true
	case "debug":
		return len(args) >= 2 && args[1] == "prompt-input"
	default:
		return false
	}
}

func maybeInjectCodexProfileOverlay(codexHome, profileName string, args []string) []string {
	if !hasCodexProfileOverlay(codexHome, profileName) {
		return args
	}
	if argsHasCodexProfile(args) {
		return args
	}
	if !codexProfileSupported(args) {
		return args
	}
	return append([]string{"--profile", profileName}, args...)
}
