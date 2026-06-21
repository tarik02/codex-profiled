package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

type options struct {
	profile     string
	sharedHome  string
	codexBinary string
}

type resolvedProfile struct {
	name   string
	source string
}

func main() {
	if err := rootCommand().Execute(); err != nil {
		var exitErr exitStatusError
		if errors.As(err, &exitErr) {
			os.Exit(int(exitErr))
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type exitStatusError int

func (e exitStatusError) Error() string {
	return fmt.Sprintf("codex exited with status %d", e)
}

func rootCommand() *cobra.Command {
	opts := defaultOptions()
	listVerbose := false
	currentVerbose := false
	deleteYes := false
	forwardVersion := false

	root := &cobra.Command{
		Use:           "codex-profiled [@profile] [--] [codex args...]",
		Short:         "Run Codex with shared state and profile-specific auth",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if forwardVersion {
				return runCodex(cmd, opts, []string{"--version"}, false)
			}
			return runCodex(cmd, opts, args, true)
		},
	}

	root.PersistentFlags().StringVarP(&opts.profile, "profile", "p", opts.profile, "profile name")
	root.PersistentFlags().StringVar(&opts.codexBinary, "codex-binary", opts.codexBinary, "codex executable")
	root.PersistentFlags().BoolVar(&forwardVersion, "version", false, "show Codex version")

	list := &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalizeOptions(&opts); err != nil {
				return err
			}
			profiles, err := listProfiles(opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, profile := range profiles {
				if listVerbose {
					if _, err := fmt.Fprintf(out, "%s\t%s\n", profile, authPathForProfile(opts, profile)); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintln(out, profile); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	list.Flags().BoolVarP(&listVerbose, "verbose", "v", false, "show auth file paths")

	current := &cobra.Command{
		Use:   "current",
		Short: "Show resolved profile for the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveProfile(opts, nil, false)
			if err != nil {
				return err
			}
			if resolved.name == "" {
				resolved.name = "default"
				resolved.source = "implicit"
			}
			if currentVerbose {
				if resolved.source == "" {
					resolved.source = "none"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", resolved.name, resolved.source); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), resolved.name); err != nil {
					return err
				}
			}
			return nil
		},
	}
	current.Flags().BoolVarP(&currentVerbose, "verbose", "v", false, "show where the profile came from")

	setDefault := &cobra.Command{
		Use:   "set-default [profile]",
		Short: "Set the current directory's default profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalizeOptions(&opts); err != nil {
				return err
			}
			if err := validateProfile(args[0]); err != nil {
				return err
			}
			path, err := setDefaultProfile(opts.sharedHome, ".", args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", args[0], path)
			return err
		},
	}

	deleteProfile := &cobra.Command{
		Use:   "delete [profile]",
		Short: "Delete a profile shadow home",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalizeOptions(&opts); err != nil {
				return err
			}

			profile := ""
			if len(args) > 0 {
				profile = args[0]
			} else {
				var err error
				profile, err = chooseDeletableProfile(opts)
				if err != nil {
					return err
				}
			}

			if err := validateProfileName(profile); err != nil {
				return err
			}
			if profile == "default" {
				return fmt.Errorf("cannot delete default profile")
			}
			if !deleteYes {
				confirmed := false
				if err := huh.NewConfirm().
					Title("delete profile " + profile + "?").
					Value(&confirmed).
					Run(); err != nil {
					return err
				}
				if !confirmed {
					return nil
				}
			}

			authPath, removedDefaults, err := deleteStoredProfile(opts, profile)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "deleted\t%s\n", authPath); err != nil {
				return err
			}
			if removedDefaults > 0 {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "removed defaults\t%d\n", removedDefaults); err != nil {
					return err
				}
			}
			return nil
		},
	}
	deleteProfile.Flags().BoolVarP(&deleteYes, "yes", "y", false, "delete without confirmation")

	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Check local requirements",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, opts)
		},
	}

	materialize := &cobra.Command{
		Use:   "materialize [@profile]",
		Short: "Create or refresh the profile shadow home links",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMaterialize(cmd, opts, args)
		},
	}

	root.AddCommand(list, current, setDefault, deleteProfile, doctor, materialize)
	for _, name := range codexPassthroughCommands() {
		root.AddCommand(codexPassthroughCommand(name, &opts))
	}
	return root
}

func codexPassthroughCommands() []string {
	return []string{
		"app",
		"app-server",
		"apply",
		"cloud",
		"debug",
		"exec",
		"exec-server",
		"features",
		"fork",
		"login",
		"logout",
		"mcp",
		"mcp-server",
		"plugin",
		"remote-control",
		"resume",
		"review",
		"sandbox",
		"update",
	}
}

func codexPassthroughCommand(name string, opts *options) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Short:              "Forward to codex " + name,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCodex(cmd, *opts, append([]string{name}, args...), false)
		},
	}
}

func defaultOptions() options {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}

	return options{
		sharedHome:  envOrDefault("CODEX_HOME", filepath.Join(home, ".codex")),
		codexBinary: envOrDefault("CODEX_BINARY", "codex"),
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func runCodex(cmd *cobra.Command, opts options, args []string, allowArgsProfile bool) error {
	if err := canonicalizeOptions(&opts); err != nil {
		return err
	}

	resolved, err := resolveProfile(opts, args, allowArgsProfile)
	if err != nil {
		return err
	}
	opts.profile = resolved.name
	argProfile := argsProfile(args)
	if opts.profile == "" {
		if len(args) > 0 {
			opts.profile = "default"
		} else {
			profile, err := chooseProfile(opts)
			if err != nil {
				return err
			}
			opts.profile = profile
		}
	} else if allowArgsProfile && opts.profile == argProfile {
		args = args[1:]
	}

	if err := validateProfile(opts.profile); err != nil {
		return err
	}

	if err := os.MkdirAll(opts.sharedHome, 0o700); err != nil {
		return err
	}

	codexBinary, err := exec.LookPath(opts.codexBinary)
	if err != nil {
		return fmt.Errorf("codex not found: %w", err)
	}

	if opts.profile == "default" {
		return runCodexProcess(opts.sharedHome, opts.profile, codexBinary, args)
	}

	layout, err := resolveShadowHomeLayout(opts, opts.profile)
	if err != nil {
		return err
	}
	if err := materializeShadowHome(layout); err != nil {
		return fmt.Errorf("failed to materialize shadow home: %w", err)
	}

	authFile := filepath.Join(layout.effectiveHomePath, "auth.json")
	if len(args) == 0 && (!fileExists(authFile) || emptyFile(authFile)) && cmd.InOrStdin() == os.Stdin {
		loginArgs, err := chooseLoginArgs()
		if err != nil {
			return err
		}
		if len(loginArgs) > 0 {
			if err := ensureShadowAuth(authFile); err != nil {
				return fmt.Errorf("failed to prepare profile auth: %w", err)
			}
			return runCodexProcess(layout.effectiveHomePath, opts.profile, codexBinary, loginArgs)
		}
		if err := writeAuthPlaceholder(authFile); err != nil {
			return err
		}
	}

	return runWithShadowHome(opts, opts.profile, codexBinary, args)
}

func canonicalizeOptions(opts *options) error {
	var err error
	opts.sharedHome, err = filepath.Abs(opts.sharedHome)
	if err != nil {
		return err
	}
	return nil
}

func resolveProfile(opts options, args []string, allowArgsProfile bool) (resolvedProfile, error) {
	if err := canonicalizeOptions(&opts); err != nil {
		return resolvedProfile{}, err
	}
	if opts.profile != "" {
		return resolvedProfile{name: opts.profile, source: "--profile"}, nil
	}
	if allowArgsProfile {
		if profile := argsProfile(args); profile != "" {
			return resolvedProfile{name: profile, source: "argument"}, nil
		}
	}
	if profile := os.Getenv("CODEX_PROFILE"); profile != "" {
		return resolvedProfile{name: profile, source: "CODEX_PROFILE"}, nil
	}
	profile, source, err := defaultProfileForDir(opts.sharedHome, ".")
	if err != nil {
		return resolvedProfile{}, err
	}
	if profile != "" {
		return resolvedProfile{name: profile, source: source}, nil
	}
	return resolvedProfile{name: "", source: ""}, nil
}

func argsProfile(args []string) string {
	if len(args) > 0 && strings.HasPrefix(args[0], "@") {
		return strings.TrimPrefix(args[0], "@")
	}
	return ""
}

type profileDefaults struct {
	Paths map[string]string `toml:"paths"`
}

func defaultProfileForDir(sharedHome string, dir string) (string, string, error) {
	configPath := filepath.Join(sharedHome, "profile-defaults.toml")
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}

	var defaults profileDefaults
	if err := toml.Unmarshal(data, &defaults); err != nil {
		return "", "", err
	}

	cwd, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", "", err
	}

	bestPath := ""
	bestProfile := ""
	for path, profile := range defaults.Paths {
		path = os.ExpandEnv(path)
		absPath, err := filepath.Abs(path)
		if err != nil {
			return "", "", err
		}
		absPath, err = filepath.EvalSymlinks(absPath)
		if err != nil {
			continue
		}
		if pathContains(absPath, cwd) && len(absPath) > len(bestPath) {
			bestPath = absPath
			bestProfile = profile
		}
	}
	if bestProfile == "" {
		return "", "", nil
	}
	return bestProfile, configPath + ":" + bestPath, nil
}

func pathContains(base string, path string) bool {
	if base == path {
		return true
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func setDefaultProfile(sharedHome string, dir string, profile string) (string, error) {
	configPath := filepath.Join(sharedHome, "profile-defaults.toml")
	defaults := profileDefaults{Paths: map[string]string{}}

	data, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if len(data) > 0 {
		if err := toml.Unmarshal(data, &defaults); err != nil {
			return "", err
		}
	}
	if defaults.Paths == nil {
		defaults.Paths = map[string]string{}
	}

	cwd, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	defaults.Paths[cwd] = profile

	data, err = toml.Marshal(defaults)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(sharedHome, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return "", err
	}
	return cwd, nil
}

func deleteStoredProfile(opts options, profile string) (string, int, error) {
	shadowHome, err := shadowHomeForProfile(opts.sharedHome, profile)
	if err != nil {
		return "", 0, err
	}
	if !fileExists(shadowHome) {
		return "", 0, fmt.Errorf("profile not found: %s", profile)
	}
	if err := os.RemoveAll(shadowHome); err != nil {
		return "", 0, err
	}

	removedDefaults, err := removeDefaultProfileRefs(opts.sharedHome, profile)
	if err != nil {
		return "", 0, err
	}
	return shadowHome, removedDefaults, nil
}

func removeDefaultProfileRefs(sharedHome string, profile string) (int, error) {
	configPath := filepath.Join(sharedHome, "profile-defaults.toml")
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var defaults profileDefaults
	if err := toml.Unmarshal(data, &defaults); err != nil {
		return 0, err
	}

	removed := 0
	for path, defaultProfile := range defaults.Paths {
		if defaultProfile == profile {
			delete(defaults.Paths, path)
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}

	data, err = toml.Marshal(defaults)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return 0, err
	}
	return removed, nil
}

func authPathForProfile(opts options, profile string) string {
	if profile == "default" {
		return filepath.Join(opts.sharedHome, "auth.json")
	}
	shadowHome, err := shadowHomeForProfile(opts.sharedHome, profile)
	if err != nil {
		return ""
	}
	return filepath.Join(shadowHome, "auth.json")
}

func chooseProfile(opts options) (string, error) {
	profiles, err := listProfiles(opts)
	if err != nil {
		return "", err
	}

	const newProfile = "<new profile>"
	choices := make([]huh.Option[string], 0, len(profiles)+1)
	for _, profile := range profiles {
		choices = append(choices, huh.NewOption(profile, profile))
	}
	choices = append(choices, huh.NewOption("new profile", newProfile))

	selected := ""
	if len(profiles) > 0 {
		if err := huh.NewSelect[string]().
			Title("profile").
			Options(choices...).
			Value(&selected).
			Run(); err != nil {
			return "", err
		}
	}

	if selected == "" || selected == newProfile {
		selected = ""
		if err := huh.NewInput().
			Title("profile name:").
			Value(&selected).
			Run(); err != nil {
			return "", err
		}
	}

	return selected, nil
}

func chooseDeletableProfile(opts options) (string, error) {
	profiles, err := listProfiles(opts)
	if err != nil {
		return "", err
	}

	choices := make([]huh.Option[string], 0, len(profiles))
	for _, profile := range profiles {
		if profile != "default" {
			choices = append(choices, huh.NewOption(profile, profile))
		}
	}
	if len(choices) == 0 {
		return "", fmt.Errorf("no profiles to delete")
	}

	selected := ""
	if err := huh.NewSelect[string]().
		Title("profile").
		Options(choices...).
		Value(&selected).
		Run(); err != nil {
		return "", err
	}
	return selected, nil
}

func chooseLoginArgs() ([]string, error) {
	value := "no"
	err := huh.NewSelect[string]().
		Title("profile has no auth. run codex login first?").
		Options(
			huh.NewOption("yes", "yes"),
			huh.NewOption("device auth", "device-auth"),
			huh.NewOption("no", "no"),
		).
		Value(&value).
		Run()
	if err != nil {
		return nil, err
	}
	switch value {
	case "yes":
		return []string{"login"}, nil
	case "device-auth":
		return []string{"login", "--device-auth"}, nil
	default:
		return nil, nil
	}
}

func listProfiles(opts options) ([]string, error) {
	discovered, err := discoverProfileNames(opts.sharedHome)
	if err != nil {
		return nil, err
	}
	profiles := append([]string{"default"}, discovered...)
	slices.Sort(profiles)
	return profiles, nil
}

func discoverProfileNames(sharedHome string) ([]string, error) {
	if root := os.Getenv("CODEX_SHADOW_ROOT"); root != "" {
		expanded := os.ExpandEnv(root)
		if expanded == "" {
			return nil, fmt.Errorf("CODEX_SHADOW_ROOT is empty")
		}
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return nil, err
		}
		return discoverProfilesInDir(abs, func(name string) string {
			return name
		})
	}

	parent := filepath.Dir(sharedHome)
	prefix := ".codex-"
	return discoverProfilesInDir(parent, func(name string) string {
		if !strings.HasPrefix(name, prefix) {
			return ""
		}
		return strings.TrimPrefix(name, prefix)
	})
}

func discoverProfilesInDir(dir string, profileFromEntry func(string) string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		profile := profileFromEntry(entry.Name())
		if profile == "" || profile == "default" {
			continue
		}
		profiles = append(profiles, profile)
	}
	slices.Sort(profiles)
	return profiles, nil
}

func validateProfile(profile string) error {
	if err := validateProfileName(profile); err != nil {
		return err
	}
	if slices.Contains([]string{"current", "delete", "doctor", "list", "materialize", "set-default"}, profile) {
		return fmt.Errorf("reserved profile name: %s", profile)
	}
	return nil
}

func validateProfileName(profile string) error {
	if profile == "" || profile == "." || profile == ".." || strings.TrimSpace(profile) == "" {
		return fmt.Errorf("invalid profile name")
	}
	for _, r := range profile {
		if invalidProfileRune(r) {
			return fmt.Errorf("invalid profile name")
		}
	}
	return nil
}

func invalidProfileRune(r rune) bool {
	if r < 0x20 {
		return true
	}
	return strings.ContainsRune(`/\<>:"|?*`, r)
}

func runWithShadowHome(opts options, profile string, codexBinary string, args []string) error {
	layout, err := resolveShadowHomeLayout(opts, profile)
	if err != nil {
		return err
	}
	if err := materializeShadowHome(layout); err != nil {
		return fmt.Errorf("failed to materialize shadow home: %w", err)
	}
	authFile := filepath.Join(layout.effectiveHomePath, "auth.json")
	if err := ensureShadowAuth(authFile); err != nil {
		return fmt.Errorf("failed to prepare profile auth: %w", err)
	}

	return runCodexProcess(layout.effectiveHomePath, profile, codexBinary, args)
}

func runMaterialize(cmd *cobra.Command, opts options, args []string) error {
	if err := canonicalizeOptions(&opts); err != nil {
		return err
	}

	resolved, err := resolveProfile(opts, args, true)
	if err != nil {
		return err
	}
	profile := resolved.name
	if profile == "" {
		if len(args) > 0 {
			profile = "default"
		} else {
			profile, err = chooseProfile(opts)
			if err != nil {
				return err
			}
		}
	}
	if err := validateProfile(profile); err != nil {
		return err
	}
	if profile == "default" {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "default profile uses shared home directly: %s\n", opts.sharedHome)
		return err
	}

	layout, err := resolveShadowHomeLayout(opts, profile)
	if err != nil {
		return err
	}
	if err := materializeShadowHome(layout); err != nil {
		return err
	}
	authFile := filepath.Join(layout.effectiveHomePath, "auth.json")
	if err := ensureShadowAuth(authFile); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", profile, layout.effectiveHomePath)
	return err
}

func runCodexProcess(codexHome string, profileName string, codexBinary string, args []string) error {
	var err error
	args, err = maybeInjectCodexProfileOverlay(codexHome, profileName, args)
	if err != nil {
		return err
	}

	child := exec.Command(codexBinary, args...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = appendEnvOverride(os.Environ(), "CODEX_HOME", codexHome)

	if err := child.Start(); err != nil {
		return err
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, forwardedSignals()...)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			if child.Process != nil {
				_ = child.Process.Signal(sig)
			}
		}
	}()

	err = child.Wait()
	if err == nil {
		return nil
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitStatusError(exitError.ExitCode())
	}
	return err
}

func appendEnvOverride(env []string, name, value string) []string {
	prefix := name + "="
	filtered := env[:0]
	for _, entry := range env {
		if envNameMatches(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, prefix+value)
}

func envNameMatches(entry, prefix string) bool {
	if len(entry) < len(prefix) {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(entry[:len(prefix)], prefix)
	}
	return strings.HasPrefix(entry, prefix)
}

func runDoctor(cmd *cobra.Command, opts options) error {
	if err := canonicalizeOptions(&opts); err != nil {
		return err
	}

	resolved, err := resolveProfile(opts, nil, false)
	if err != nil {
		return err
	}
	if resolved.name == "" {
		resolved.name = "default"
		resolved.source = "implicit"
	}
	if resolved.source == "" {
		resolved.source = "none"
	}

	printCheck := func(name string, value string, ok bool) error {
		status := "ok"
		if !ok {
			status = "missing"
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", status, name, value)
		return err
	}

	codexPath, err := exec.LookPath(opts.codexBinary)
	if err := printCheck("codex", codexPath, err == nil); err != nil {
		return err
	}

	shadowHome := opts.sharedHome
	shadowOK := true
	if resolved.name != "" && resolved.name != "default" {
		layout, layoutErr := resolveShadowHomeLayout(opts, resolved.name)
		if layoutErr == nil {
			shadowHome = layout.effectiveHomePath
			shadowOK = shadowHomeExists(layout)
		}
	}
	if err := printCheck("shadow home", shadowHome, shadowOK || resolved.name == "default"); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "info\tCODEX_HOME\t%s\n", opts.sharedHome); err != nil {
		return err
	}
	if resolved.name != "" && resolved.name != "default" {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "info\tprofile auth\t%s\n", authPathForProfile(opts, resolved.name)); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "info\tcurrent profile\t%s (%s)\n", resolved.name, resolved.source)
	return err
}
