package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
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

func maybeInjectCodexProfileOverlay(codexHome, profileName string, args []string) ([]string, error) {
	if !hasCodexProfileOverlay(codexHome, profileName) {
		return args, nil
	}
	if argsHasCodexProfile(args) {
		return args, nil
	}
	if codexProfileSupported(args) {
		return append([]string{"--profile", profileName}, args...), nil
	}

	configArgs, err := codexProfileOverlayConfigArgs(codexOverlayConfigPath(codexHome, profileName))
	if err != nil {
		return nil, err
	}
	return append(args, configArgs...), nil
}

func codexProfileOverlayConfigArgs(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read codex profile overlay: %w", err)
	}

	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse codex profile overlay: %w", err)
	}

	overrides, err := flattenCodexConfig(config)
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, len(overrides)*2)
	for _, override := range overrides {
		args = append(args, "--config", override)
	}
	return args, nil
}

func flattenCodexConfig(config map[string]any) ([]string, error) {
	overrides := []string{}
	if err := appendFlattenedCodexConfig(nil, config, &overrides); err != nil {
		return nil, err
	}
	return overrides, nil
}

func appendFlattenedCodexConfig(prefix []string, config map[string]any, overrides *[]string) error {
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		path := append(prefix, key)
		value := config[key]
		if nested, ok := value.(map[string]any); ok {
			if err := appendFlattenedCodexConfig(path, nested, overrides); err != nil {
				return err
			}
			continue
		}

		encoded, err := encodeCodexConfigValue(value)
		if err != nil {
			return fmt.Errorf("encode codex config %s: %w", codexConfigPath(path), err)
		}
		*overrides = append(*overrides, codexConfigPath(path)+"="+encoded)
	}

	return nil
}

func encodeCodexConfigValue(value any) (string, error) {
	var buf bytes.Buffer
	err := toml.NewEncoder(&buf).
		SetArraysMultiline(false).
		SetTablesInline(true).
		Encode(map[string]any{"value": value})
	if err != nil {
		return "", err
	}

	const prefix = "value = "
	encoded := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(encoded, prefix) {
		return "", fmt.Errorf("unexpected encoded TOML value: %s", encoded)
	}
	return strings.TrimSpace(strings.TrimPrefix(encoded, prefix)), nil
}

func codexConfigPath(parts []string) string {
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		encoded = append(encoded, codexConfigPathPart(part))
	}
	return strings.Join(encoded, ".")
}

func codexConfigPathPart(part string) string {
	if part != "" {
		simple := true
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			simple = false
			break
		}
		if simple {
			return part
		}
	}
	return strconv.Quote(part)
}
