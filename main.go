package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/charmbracelet/huh"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

type options struct {
	profile     string
	profileRoot string
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

	root := &cobra.Command{
		Use:           "codex-profiled [@profile] [--] [codex args...]",
		Short:         "Run Codex with shared state and profile-specific auth",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCodex(cmd, opts, args, true)
		},
	}

	root.PersistentFlags().StringVarP(&opts.profile, "profile", "p", opts.profile, "profile name")
	root.PersistentFlags().StringVar(&opts.profileRoot, "profile-root", opts.profileRoot, "profile root (default: CODEX_HOME/auth-profiles)")
	root.PersistentFlags().StringVar(&opts.codexBinary, "codex-binary", opts.codexBinary, "codex executable")

	list := &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fillDerivedDefaults(&opts)
			if err := canonicalizeOptions(&opts); err != nil {
				return err
			}
			profiles, err := listProfiles(opts.profileRoot)
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
			fillDerivedDefaults(&opts)
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
		Short: "Delete a profile auth file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fillDerivedDefaults(&opts)
			if err := canonicalizeOptions(&opts); err != nil {
				return err
			}

			profile := ""
			if len(args) > 0 {
				profile = args[0]
			} else {
				var err error
				profile, err = chooseDeletableProfile(opts.profileRoot)
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

	root.AddCommand(list, current, setDefault, deleteProfile, doctor)
	return root
}

func defaultOptions() options {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}

	return options{
		sharedHome:  envOrDefault("CODEX_HOME", filepath.Join(home, ".codex")),
		profileRoot: os.Getenv("CODEX_PROFILE_ROOT"),
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
	fillDerivedDefaults(&opts)

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
			profile, err := chooseProfile(opts.profileRoot)
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
		return runCodexProcess(opts.sharedHome, codexBinary, args)
	}

	authFile := authPathForProfile(opts, opts.profile)
	if err := os.MkdirAll(opts.profileRoot, 0o700); err != nil {
		return err
	}
	if err := ensureFile(authFile, 0o600); err != nil {
		return err
	}

	if len(args) == 0 && emptyFile(authFile) && cmd.InOrStdin() == os.Stdin {
		loginArgs, err := chooseLoginArgs()
		if err != nil {
			return err
		}
		if len(loginArgs) > 0 {
			if err := runWithMountedHome(opts, authFile, codexBinary, loginArgs); err != nil {
				return err
			}
		}
	}

	return runWithMountedHome(opts, authFile, codexBinary, args)
}

func fillDerivedDefaults(opts *options) {
	if opts.profileRoot == "" {
		opts.profileRoot = filepath.Join(opts.sharedHome, "auth-profiles")
	}
}

func canonicalizeOptions(opts *options) error {
	var err error
	opts.sharedHome, err = filepath.Abs(opts.sharedHome)
	if err != nil {
		return err
	}
	opts.profileRoot, err = filepath.Abs(opts.profileRoot)
	if err != nil {
		return err
	}
	return nil
}

func resolveProfile(opts options, args []string, allowArgsProfile bool) (resolvedProfile, error) {
	fillDerivedDefaults(&opts)
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
	authPath := authPathForProfile(opts, profile)
	if err := os.Remove(authPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", 0, fmt.Errorf("profile not found: %s", profile)
		}
		return "", 0, err
	}

	removedDefaults, err := removeDefaultProfileRefs(opts.sharedHome, profile)
	if err != nil {
		return "", 0, err
	}
	return authPath, removedDefaults, nil
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
	return filepath.Join(opts.profileRoot, profile+".auth.json")
}

func chooseProfile(profileRoot string) (string, error) {
	profiles, err := listProfiles(profileRoot)
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

func chooseDeletableProfile(profileRoot string) (string, error) {
	profiles, err := listProfiles(profileRoot)
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

func listProfiles(profileRoot string) ([]string, error) {
	entries, err := os.ReadDir(profileRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []string{"default"}, nil
	}
	if err != nil {
		return nil, err
	}

	profiles := []string{"default"}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".auth.json") {
			profile := strings.TrimSuffix(name, ".auth.json")
			if profile != "default" {
				profiles = append(profiles, profile)
			}
		}
	}
	slices.Sort(profiles)
	return profiles, nil
}

func validateProfile(profile string) error {
	if err := validateProfileName(profile); err != nil {
		return err
	}
	if slices.Contains([]string{"current", "delete", "doctor", "list", "set-default"}, profile) {
		return fmt.Errorf("reserved profile name: %s", profile)
	}
	return nil
}

func validateProfileName(profile string) error {
	if profile == "" || profile == "." || profile == ".." || strings.TrimSpace(profile) == "" || strings.ContainsRune(profile, filepath.Separator) {
		return fmt.Errorf("invalid profile name")
	}
	return nil
}

func ensureFile(path string, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, mode)
	if err != nil {
		return err
	}
	return file.Close()
}

func emptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() == 0
}

func runWithMountedHome(opts options, authFile string, codexBinary string, args []string) error {
	runtimeDir := filepath.Join(opts.sharedHome, ".profile-runtimes")
	runtimeDir, err := filepath.Abs(runtimeDir)
	if err != nil {
		return err
	}

	sessionDir, err := os.MkdirTemp(runtimeDir, opts.profile+"-")
	if err != nil {
		if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
			return err
		}
		sessionDir, err = os.MkdirTemp(runtimeDir, opts.profile+"-")
		if err != nil {
			return err
		}
	}
	defer func() {
		_ = os.RemoveAll(sessionDir)
	}()

	mountpoint := filepath.Join(sessionDir, "home")
	if err := os.Mkdir(mountpoint, 0o700); err != nil {
		return err
	}

	server, err := mountProfileHome(mountpoint, opts.sharedHome, authFile)
	if err != nil {
		return fmt.Errorf("failed to mount FUSE profile home: %w\ncheck: /dev/fuse exists, fusermount3 is available, NoNewPrivs is 0", err)
	}
	defer func() {
		_ = server.Unmount()
	}()

	return runCodexProcess(mountpoint, codexBinary, args)
}

func runCodexProcess(codexHome string, codexBinary string, args []string) error {
	child := exec.Command(codexBinary, args...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = append(os.Environ(), "CODEX_HOME="+codexHome)

	if err := child.Start(); err != nil {
		return err
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			if child.Process != nil {
				_ = child.Process.Signal(sig)
			}
		}
	}()

	err := child.Wait()
	if err == nil {
		return nil
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
			return exitStatusError(status.ExitStatus())
		}
	}
	return err
}

func runDoctor(cmd *cobra.Command, opts options) error {
	fillDerivedDefaults(&opts)
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

	fusermountPath, err := exec.LookPath("fusermount3")
	if err != nil {
		fusermountPath, err = exec.LookPath("fusermount")
	}
	if err := printCheck("fusermount", fusermountPath, err == nil); err != nil {
		return err
	}

	if info, err := os.Stat("/dev/fuse"); err == nil {
		if err := printCheck("/dev/fuse", info.Mode().String(), true); err != nil {
			return err
		}
	} else {
		if err := printCheck("/dev/fuse", err.Error(), false); err != nil {
			return err
		}
	}

	noNewPrivs := "unknown"
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "NoNewPrivs:") {
				noNewPrivs = strings.TrimSpace(strings.TrimPrefix(line, "NoNewPrivs:"))
				break
			}
		}
	}
	if err := printCheck("NoNewPrivs", noNewPrivs, noNewPrivs == "0"); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "info\tCODEX_HOME\t%s\n", opts.sharedHome); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "info\tprofile root\t%s\n", opts.profileRoot); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "info\tcurrent profile\t%s (%s)\n", resolved.name, resolved.source)
	return err
}
