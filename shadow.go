package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

var (
	knownSharedDirectories = []string{
		"sessions",
		"archived_sessions",
		"sqlite",
		"shell_snapshots",
		"worktrees",
		"skills",
		"plugins",
		"cache",
		"logs",
	}

	privateEntryNames = map[string]struct{}{
		"auth.json":         {},
		"models_cache.json": {},
	}

	shadowLocalEntryNames = map[string]struct{}{
		"log":      {},
		"memories": {},
		"tmp":      {},
	}

	skippedSharedEntryNames = map[string]struct{}{
		"auth-profiles": {},
	}

	// optionalLocalEntryNames are symlinked from shared when missing, but an
	// existing real file in the shadow home is preserved.
	optionalLocalEntryNames = map[string]struct{}{
		"config.toml": {},
	}

	emptyAuthJSON = []byte("{}")
)

const (
	windowsErrorSharingViolation syscall.Errno = 32
	windowsErrorNotAReparsePoint syscall.Errno = 4390
)

type linkState int

const (
	linkMissing linkState = iota
	linkNotSymlink
	linkSymlink
)

type shadowHomeLayout struct {
	sharedHomePath    string
	effectiveHomePath string
}

func shadowHomeForProfile(sharedHome, profile string) (string, error) {
	if root := os.Getenv("CODEX_SHADOW_ROOT"); root != "" {
		expanded := os.ExpandEnv(root)
		if expanded == "" {
			return "", fmt.Errorf("CODEX_SHADOW_ROOT is empty")
		}
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return "", err
		}
		return filepath.Join(abs, profile), nil
	}

	parent := filepath.Dir(sharedHome)
	return filepath.Join(parent, ".codex-"+profile), nil
}

func resolveShadowHomeLayout(opts options, profile string) (shadowHomeLayout, error) {
	shadowHome, err := shadowHomeForProfile(opts.sharedHome, profile)
	if err != nil {
		return shadowHomeLayout{}, err
	}
	shadowHome, err = filepath.Abs(shadowHome)
	if err != nil {
		return shadowHomeLayout{}, err
	}

	return shadowHomeLayout{
		sharedHomePath:    opts.sharedHome,
		effectiveHomePath: shadowHome,
	}, nil
}

func materializeShadowHome(layout shadowHomeLayout) error {
	if layout.sharedHomePath == layout.effectiveHomePath {
		return fmt.Errorf("shadow home path must differ from shared home path")
	}

	if err := os.MkdirAll(layout.sharedHomePath, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(layout.effectiveHomePath, 0o700); err != nil {
		return err
	}

	for _, directory := range knownSharedDirectories {
		path := filepath.Join(layout.sharedHomePath, directory)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}

	sharedEntries, err := os.ReadDir(layout.sharedHomePath)
	if err != nil {
		return err
	}

	entries := make(map[string]struct{}, len(knownSharedDirectories)+len(sharedEntries))
	for _, directory := range knownSharedDirectories {
		entries[directory] = struct{}{}
	}
	for _, entry := range sharedEntries {
		name := entry.Name()
		if _, private := privateEntryNames[name]; private {
			continue
		}
		if isSQLiteSidecarName(name) {
			continue
		}
		if _, local := shadowLocalEntryNames[name]; local {
			continue
		}
		if _, skip := skippedSharedEntryNames[name]; skip {
			continue
		}
		if _, optional := optionalLocalEntryNames[name]; optional {
			state, _, err := readLinkState(filepath.Join(layout.effectiveHomePath, name))
			if err != nil {
				return err
			}
			if state == linkNotSymlink {
				continue
			}
		}
		entries[name] = struct{}{}
		if isSQLiteDatabaseName(name) && !entry.IsDir() {
			for _, sidecarName := range sqliteSidecarNames(name) {
				entries[sidecarName] = struct{}{}
			}
		}
	}

	if err := removePrivateSymlink(layout.effectiveHomePath, "models_cache.json"); err != nil {
		return err
	}
	for name := range skippedSharedEntryNames {
		if err := removePrivateSymlink(layout.effectiveHomePath, name); err != nil {
			return err
		}
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)

	if err := ensureSharedSQLiteSidecars(layout.sharedHomePath, names); err != nil {
		return err
	}

	for _, name := range names {
		if _, private := privateEntryNames[name]; private {
			continue
		}
		if _, skip := skippedSharedEntryNames[name]; skip {
			continue
		}
		if err := ensureSharedSymlink(layout.effectiveHomePath, layout.sharedHomePath, name); err != nil {
			return err
		}
	}

	return ensureShadowAuthIsPrivate(layout.effectiveHomePath)
}

func isSQLiteDatabaseName(name string) bool {
	return strings.HasSuffix(name, ".sqlite")
}

func isSQLiteSidecarName(name string) bool {
	return strings.HasSuffix(name, ".sqlite-shm") || strings.HasSuffix(name, ".sqlite-wal")
}

func sqliteSidecarNames(name string) []string {
	return []string{name + "-shm", name + "-wal"}
}

func ensureSharedSQLiteSidecars(sharedPath string, names []string) error {
	for _, name := range names {
		if !isSQLiteSidecarName(name) {
			continue
		}
		file, err := os.OpenFile(filepath.Join(sharedPath, name), os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

func shadowHomeExists(layout shadowHomeLayout) bool {
	if layout.sharedHomePath == layout.effectiveHomePath {
		return false
	}
	info, err := os.Stat(layout.effectiveHomePath)
	return err == nil && info.IsDir()
}

func readLinkState(linkPath string) (linkState, string, error) {
	target, err := os.Readlink(linkPath)
	if err == nil {
		return linkSymlink, target, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return linkMissing, "", nil
	}
	if isNotSymlinkError(err) {
		return linkNotSymlink, "", nil
	}
	return linkMissing, "", err
}

func isNotSymlinkError(err error) bool {
	if errors.Is(err, syscall.EINVAL) {
		return true
	}
	if runtime.GOOS == "windows" && errors.Is(err, windowsErrorNotAReparsePoint) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, syscall.EINVAL) {
			return true
		}
		if runtime.GOOS == "windows" && errors.Is(pathErr.Err, windowsErrorNotAReparsePoint) {
			return true
		}
	}
	return false
}

func removePrivateSymlink(shadowPath, entryName string) error {
	privatePath := filepath.Join(shadowPath, entryName)
	state, _, err := readLinkState(privatePath)
	if err != nil {
		return err
	}
	if state == linkSymlink {
		return os.Remove(privatePath)
	}
	return nil
}

func ensureSharedSymlink(shadowPath, sharedPath, entryName string) error {
	target := filepath.Join(sharedPath, entryName)
	link := filepath.Join(shadowPath, entryName)

	state, existingTarget, err := readLinkState(link)
	if err != nil {
		return err
	}

	switch state {
	case linkNotSymlink:
		if sameFilesystemEntry(link, target) {
			return nil
		}
		if isSQLiteSidecarName(entryName) {
			if err := os.Remove(link); err != nil {
				if isFileInUseError(err) {
					return fmt.Errorf("cannot replace local sqlite sidecar %q while it is in use; stop running Codex processes for this profile and retry: %w", link, err)
				}
				return err
			}
			return createSharedLink(target, link)
		}
		return fmt.Errorf("cannot create shadow home because %q already exists and is not a symlink", link)
	case linkMissing:
		return createSharedLink(target, link)
	case linkSymlink:
		resolvedExisting := filepath.Clean(filepath.Join(filepath.Dir(link), existingTarget))
		if resolvedExisting == filepath.Clean(target) {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
		return createSharedLink(target, link)
	default:
		return fmt.Errorf("unexpected link state for %q", link)
	}
}

func sameFilesystemEntry(pathA, pathB string) bool {
	infoA, err := os.Stat(pathA)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(pathB)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}

func isFileInUseError(err error) bool {
	if runtime.GOOS == "windows" && errors.Is(err, windowsErrorSharingViolation) {
		return true
	}
	var pathErr *os.PathError
	return errors.As(err, &pathErr) &&
		runtime.GOOS == "windows" &&
		errors.Is(pathErr.Err, windowsErrorSharingViolation)
}
func createSharedLink(target, link string) error {
	if err := os.Symlink(target, link); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}

	info, statErr := os.Stat(target)
	if statErr != nil {
		return statErr
	}
	if info.IsDir() {
		return createWindowsDirectoryJunction(target, link)
	}
	return os.Link(target, link)
}

func createWindowsDirectoryJunction(target, link string) error {
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create directory junction: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func ensureShadowAuthIsPrivate(shadowPath string) error {
	authPath := filepath.Join(shadowPath, "auth.json")
	state, _, err := readLinkState(authPath)
	if err != nil {
		return err
	}
	if state == linkSymlink {
		return fmt.Errorf("shadow auth file %q must be a real file, not a symlink", authPath)
	}
	return nil
}

func ensureShadowAuth(shadowAuthPath string) error {
	if !fileExists(shadowAuthPath) {
		return writeAuthPlaceholder(shadowAuthPath)
	}
	if emptyFile(shadowAuthPath) {
		return writeAuthPlaceholder(shadowAuthPath)
	}
	return nil
}

func emptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() == 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeAuthPlaceholder(path string) error {
	return os.WriteFile(path, emptyAuthJSON, 0o600)
}
