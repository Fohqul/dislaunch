package dislaunch

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func download(source string, destination string, progress func(progress uint8)) error {
	// TODO

	return nil
}

type Release string

const (
	Stable Release = "stable"
	PTB    Release = "ptb"
	Canary Release = "canary"
)

type BetterDiscordChannel string

const (
	BDStable BetterDiscordChannel = "stable"
	BDCanary BetterDiscordChannel = "canary"
)

type ReleaseInternal struct {
	InstallPath          string               `json:"install_path"`
	LastChecked          time.Time            `json:"last_checked"`
	LatestVersion        string               `json:"latest_version"`
	BetterDiscordEnabled bool                 `json:"bd_enabled"`
	BetterDiscordChannel BetterDiscordChannel `json:"bd_channel"`
}

func (release Release) getGobPath() string {
	return filepath.Join(GetHomeXDGDirectory("XDG_STATE_HOME", filepath.Join(".local", "state")), string(release)+".gob")
}

func (release Release) IsInstalled() bool {
	_, err := os.Stat(release.getGobPath())
	if err != nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	log.Fatalf("failed to stat gob for release '%s'\n", release)
	return false
}

func (release Release) openGob() *os.File {
	path := release.getGobPath()
	file, err := os.Open(path)
	if err == nil {
		return file
	}
	if !errors.Is(err, fs.ErrNotExist) {
		log.Fatal(err)
	}
	file, err = os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	if err = gob.NewEncoder(file).Encode(ReleaseInternal{}); err != nil {
		log.Fatal(err)
	}
	return file
}

func (release Release) setInternal(internal ReleaseInternal) {
	file := release.openGob()
	defer file.Close()

	if err := gob.NewEncoder(file).Encode(internal); err != nil {
		log.Fatal(err)
	}
}

func (release Release) GetInternal() (ReleaseInternal, error) {
	if !release.IsInstalled() {
		return ReleaseInternal{}, fmt.Errorf("release '%s' is not installed", release)
	}

	file := release.openGob()
	defer file.Close()

	var internal ReleaseInternal
	if err := gob.NewDecoder(file).Decode(&internal); err != nil {
		process := release.GetProcess()
		process.mu.Lock()
		defer process.mu.Unlock()
		process.status = Fatal
		process.err = err
		return ReleaseInternal{}, err
	}
	return internal, nil
}

func (release Release) setLastChecked(lastChecked time.Time) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	internal.LastChecked = lastChecked
	release.setInternal(internal)
}

func (release Release) SetBetterDiscordEnabled(betterDiscordEnabled bool) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	internal.BetterDiscordEnabled = betterDiscordEnabled
	release.setInternal(internal)
}

func (release Release) SetBetterDiscordChannel(betterDiscordChannel BetterDiscordChannel) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	internal.BetterDiscordChannel = betterDiscordChannel
	release.setInternal(internal)
}

/**
 * Since Discord installs expose their version in `resources/build_info.json`,
 * we can always just read from there to get the installed version without the
 * need to keep track of it ourselves.
 */

type buildInfo struct {
	Version        string  `json:"version"`
	ReleaseChannel Release `json:"releaseChannel"`
}

func (release Release) GetVersion() (string, error) {
	internal, err := release.GetInternal()
	if err != nil {
		return "", err
	}

	file, err := os.Open(filepath.Join(internal.InstallPath, "resources", "build_info.json"))
	if err != nil {
		return "", err
	}
	defer file.Close()

	var info buildInfo
	err = json.NewDecoder(file).Decode(&info)
	if err != nil {
		return "", err
	}
	if info.ReleaseChannel != release {
		process := release.GetProcess()
		process.mu.Lock()
		defer process.mu.Unlock()
		process.status = Fatal
		process.err = fmt.Errorf("mismatched release channel: %s", info.ReleaseChannel)
		return "", process.err
	}
	return info.Version, nil
}

type Status string

const (
	None                   Status = ""
	Download               Status = "download"
	Install                Status = "install"
	UpdateCheck            Status = "update_check"
	BetterDiscordInjection Status = "bd_injection"
	Move                   Status = "move"
	Uninstall              Status = "uninstall"
	Fatal                  Status = "fatal"
)

type releaseProcess struct {
	mu             sync.Mutex
	status         Status
	progressWindow bool
	message        string
	progress       uint8 // indeterminate progress when 101
	err            error
}

type ReleaseProcessView struct {
	Status         string `json:"status"`
	ProgressWindow bool   `json:"progress_window"`
	Message        string `json:"message"`
	Progress       uint8  `json:"progress"`
	Error          string `json:"error"`
}

func (process *releaseProcess) View() ReleaseProcessView {
	// HACK: can't acquire the mutex because that would prevent viewing while a process is ongoing
	// this leads to a data race but hopefully that only leads to a stale frontend
	errorMessage := ""
	if err := process.err; err != nil {
		errorMessage = err.Error()
	}

	return ReleaseProcessView{
		Status:         string(process.status),
		ProgressWindow: process.progressWindow,
		Message:        process.message,
		Progress:       process.progress,
		Error:          errorMessage,
	}
}

type releaseProcesses struct {
	stable releaseProcess
	ptb    releaseProcess
	canary releaseProcess
}

var processes releaseProcesses

func (release Release) GetProcess() *releaseProcess {
	switch release {
	case Stable:
		return &processes.stable
	case PTB:
		return &processes.ptb
	case Canary:
		return &processes.canary
	}

	log.Fatalf("unknown release: %s\n", release)
	return nil
}

func (release Release) CheckForUpdates() {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	process := release.GetProcess()
	if process.status == Fatal {
		return
	}
	process.mu.Lock()
	defer process.mu.Unlock()

	process.status = UpdateCheck
	process.message = "Checking for updates"
	process.progress = 101

	url := "https://discord.com/api/" + release + "/updates?platform=linux"
	// TODO
}

func (release Release) Install() {
	internal, err := release.GetInternal()
	if release.IsInstalled() && err != nil {
		return
	}

	process := release.GetProcess()
	if process.status == Fatal {
		return
	}
	process.mu.Lock()
	defer process.mu.Unlock()

	// TODO
}

func (release Release) InjectBetterDiscord(channel BetterDiscordChannel) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	process := release.GetProcess()
	if process.status == Fatal {
		return
	}
	process.mu.Lock()
	defer process.mu.Unlock()

	// TODO
}

func (release Release) Move(path string) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	process := release.GetProcess()
	if process.status == Fatal {
		return
	}
	process.mu.Lock()
	defer process.mu.Unlock()

	err = os.Rename(internal.InstallPath, path)
	if err == nil {
		internal.InstallPath = path
		release.setInternal(internal)
		return
	}

	// TODO handle cross-device moves
	fmt.Fprintf(os.Stderr, "error moving release '%s' to '%s': %w\n", release, path, err)
}

func (release Release) Uninstall() {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	process := release.GetProcess()
	if process.status == Fatal {
		return
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	process.status = Uninstall
	process.message = "Deleting " + internal.InstallPath
	process.progress = 101
	BroadcastGlobalState()

	// Scary!
	// todo perhaps consider some safeguards to prevent deleting critical directories?
	if err = os.RemoveAll(internal.InstallPath); err != nil {
		fmt.Fprintf(os.Stderr, "error uninstalling release '%s' from '%s': %w\n", release, internal.InstallPath, err)
		process.status = Fatal
		process.err = err
	} else if err = os.Remove(release.getGobPath()); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting gob for release '%s': %w\n", release, err)
		process.status = Fatal
		process.err = err
	}
	BroadcastGlobalState()
}
