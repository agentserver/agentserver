//go:build linux

package harnessworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/agentserver/agentserver/v2/internal/harnesslayout"
	"golang.org/x/sys/unix"
)

func validateLocalWorkerIdentity(workerUID, workerGID, appUID, appGID uint32) error {
	realUID, effectiveUID, savedUID := unix.Getresuid()
	if uint32(realUID) != workerUID || uint32(effectiveUID) != workerUID || uint32(savedUID) != workerUID {
		return fmt.Errorf("local worker uid = real %d effective %d saved %d, want %d", realUID, effectiveUID, savedUID, workerUID)
	}
	realGID, effectiveGID, savedGID := unix.Getresgid()
	if uint32(realGID) != workerGID || uint32(effectiveGID) != workerGID || uint32(savedGID) != workerGID {
		return fmt.Errorf("local worker gid = real %d effective %d saved %d, want %d", realGID, effectiveGID, savedGID, workerGID)
	}
	groups, err := os.Getgroups()
	if err != nil {
		return fmt.Errorf("read local worker supplementary groups: %w", err)
	}
	if len(groups) != 0 {
		return fmt.Errorf("local worker inherited supplementary groups %v", groups)
	}
	if workerUID == appUID || workerGID == appGID {
		return errors.New("local worker and app identities must be distinct")
	}
	return nil
}

func installLocalAppRuntime(
	ctx context.Context,
	root string,
	config []byte,
	restored *RestoredCheckpoint,
	appUID, appGID uint32,
) (paths localAppRuntimePaths, rolloutPath string, err error) {
	if err := ctx.Err(); err != nil {
		return localAppRuntimePaths{}, "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return localAppRuntimePaths{}, "", fmt.Errorf("read local app runtime anchor: %w", err)
	}
	if len(entries) != 0 {
		return localAppRuntimePaths{}, "", errors.New("local app runtime anchor must be empty")
	}
	paths = localAppRuntimePaths{
		Home: filepath.Join(root, "home"), CodexHome: filepath.Join(root, harnesslayout.CodexHomeDirectory),
		Temporary: filepath.Join(root, "tmp"), CWD: filepath.Join(root, "cwd"),
	}
	var rolloutSource *os.File
	if restored != nil {
		rolloutSource, err = os.Open(restored.RolloutPath)
		if err != nil {
			return localAppRuntimePaths{}, "", fmt.Errorf("open verified checkpoint rollout staging: %w", err)
		}
		defer rolloutSource.Close()
	}
	err = withLocalFilesystemIdentity(appUID, appGID, func() error {
		for label, path := range map[string]string{
			"home": paths.Home, "Codex home": paths.CodexHome,
			"temporary directory": paths.Temporary, "working directory": paths.CWD,
		} {
			if err := os.Mkdir(path, 0o700); err != nil {
				return fmt.Errorf("create app-owned %s: %w", label, err)
			}
		}
		configFile, err := os.OpenFile(filepath.Join(paths.CodexHome, "config.toml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create app-owned Codex config: %w", err)
		}
		written, writeErr := configFile.Write(config)
		if writeErr == nil && written != len(config) {
			writeErr = io.ErrShortWrite
		}
		if writeErr == nil {
			writeErr = configFile.Sync()
		}
		closeErr := configFile.Close()
		if writeErr != nil {
			return errors.Join(fmt.Errorf("write app-owned Codex config: %w", writeErr), closeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close app-owned Codex config: %w", closeErr)
		}
		if restored != nil {
			rolloutPath, err = restoreCheckpointRollout(paths.CodexHome, restored.Manifest.Files[0], rolloutSource)
			if err != nil {
				return fmt.Errorf("install app-owned checkpoint rollout: %w", err)
			}
		}
		if err := os.Chmod(paths.CWD, 0o555); err != nil {
			return fmt.Errorf("make app-server working directory read-only: %w", err)
		}
		return nil
	})
	if err != nil {
		return localAppRuntimePaths{}, "", err
	}
	if err := os.Chmod(root, 0o511); err != nil {
		return localAppRuntimePaths{}, "", fmt.Errorf("seal local app runtime anchor: %w", err)
	}
	return paths, rolloutPath, nil
}

func withLocalFilesystemIdentity(uid, gid uint32, action func() error) (result error) {
	if action == nil {
		return errors.New("local app filesystem action is required")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previousGID, err := unix.SetfsgidRetGid(int(gid))
	if err != nil {
		return fmt.Errorf("enter app filesystem gid %d: %w", gid, err)
	}
	defer func() {
		_, restoreErr := unix.SetfsgidRetGid(previousGID)
		if restoreErr == nil {
			current, queryErr := unix.SetfsgidRetGid(-1)
			if queryErr != nil || current != previousGID {
				restoreErr = fmt.Errorf("verify restored filesystem gid %d: current %d: %w", previousGID, current, queryErr)
			}
		}
		result = errors.Join(result, restoreErr)
	}()
	currentGID, err := unix.SetfsgidRetGid(-1)
	if err != nil || currentGID != int(gid) {
		return fmt.Errorf("verify app filesystem gid %d: current %d: %w", gid, currentGID, err)
	}
	previousUID, err := unix.SetfsuidRetUid(int(uid))
	if err != nil {
		return fmt.Errorf("enter app filesystem uid %d: %w", uid, err)
	}
	defer func() {
		_, restoreErr := unix.SetfsuidRetUid(previousUID)
		if restoreErr == nil {
			current, queryErr := unix.SetfsuidRetUid(-1)
			if queryErr != nil || current != previousUID {
				restoreErr = fmt.Errorf("verify restored filesystem uid %d: current %d: %w", previousUID, current, queryErr)
			}
		}
		result = errors.Join(result, restoreErr)
	}()
	currentUID, err := unix.SetfsuidRetUid(-1)
	if err != nil || currentUID != int(uid) {
		return fmt.Errorf("verify app filesystem uid %d: current %d: %w", uid, currentUID, err)
	}
	return action()
}
