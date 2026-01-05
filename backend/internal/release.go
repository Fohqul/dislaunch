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

type Release struct {
	mu             sync.Mutex
	status         Status
	progressWindow bool
	message        string
	progress       uint8 // indeterminate progress when 101
	err            error
}

var Stable, PTB, Canary Release

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

func (release *Release) String() string {
	switch *release {
	case Stable:
		return "stable"
	case PTB:
		return "ptb"
	case Canary:
		return "canary"
	}

	log.Fatalf("unknown release: %p\n", release)
	return ""
}

func (release *Release) getGobPath() string {
	return filepath.Join(GetHomeXDGDirectory("XDG_STATE_HOME", filepath.Join(".local", "state")), release.String()+".gob")
}

func (release *Release) IsInstalled() bool {
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

func (release *Release) openGob() *os.File {
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

func (release *Release) setInternal(internal ReleaseInternal) {
	file := release.openGob()
	defer file.Close()

	if err := gob.NewEncoder(file).Encode(internal); err != nil {
		log.Fatal(err)
	}
}

func (release *Release) GetInternal() (ReleaseInternal, error) {
	if !release.IsInstalled() {
		return ReleaseInternal{}, fmt.Errorf("release '%s' is not installed", release)
	}

	file := release.openGob()
	defer file.Close()

	var internal ReleaseInternal
	if err := gob.NewDecoder(file).Decode(&internal); err != nil {
		release.mu.Lock()
		defer release.mu.Unlock()
		release.status = Fatal
		release.err = err
		return ReleaseInternal{}, err
	}
	return internal, nil
}

func (release *Release) setLastChecked(lastChecked time.Time) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	internal.LastChecked = lastChecked
	release.setInternal(internal)
}

func (release *Release) SetBetterDiscordEnabled(betterDiscordEnabled bool) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	internal.BetterDiscordEnabled = betterDiscordEnabled
	release.setInternal(internal)
}

func (release *Release) SetBetterDiscordChannel(betterDiscordChannel BetterDiscordChannel) {
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
	Version        string `json:"version"`
	ReleaseChannel string `json:"releaseChannel"`
}

func (release *Release) GetVersion() (string, error) {
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
	if info.ReleaseChannel != release.String() {
		release.mu.Lock()
		defer release.mu.Unlock()
		release.status = Fatal
		release.err = fmt.Errorf("mismatched release channel: %s", info.ReleaseChannel)
		return "", release.err
	}
	return info.Version, nil
}

type ReleaseProcessView struct {
	Status         string `json:"status"`
	ProgressWindow bool   `json:"progress_window"`
	Message        string `json:"message"`
	Progress       uint8  `json:"progress"`
	Error          string `json:"error"`
}

func (release *Release) View() ReleaseProcessView {
	// HACK: can't acquire the mutex because that would prevent viewing while a process is ongoing
	// this leads to a data race but hopefully that only leads to a stale frontend
	errorMessage := ""
	if err := release.err; err != nil {
		errorMessage = err.Error()
	}

	return ReleaseProcessView{
		Status:         string(release.status),
		ProgressWindow: release.progressWindow,
		Message:        release.message,
		Progress:       release.progress,
		Error:          errorMessage,
	}
}

func (release *Release) CheckForUpdates() {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	if release.status == Fatal {
		return
	}
	release.mu.Lock()
	defer release.mu.Unlock()

	release.status = UpdateCheck
	release.message = "Checking for updates"
	release.progress = 101

	url := "https://discord.com/api/" + release.String() + "/updates?platform=linux"
	// TODO
}

func (release *Release) Install() {
	internal, err := release.GetInternal()
	if release.IsInstalled() && err != nil {
		return
	}

	if release.status == Fatal {
		return
	}
	release.mu.Lock()
	defer release.mu.Unlock()

	// TODO
}

func (release *Release) InjectBetterDiscord(channel BetterDiscordChannel) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	if release.status == Fatal {
		return
	}
	release.mu.Lock()
	defer release.mu.Unlock()

	// TODO
}

func (release *Release) Move(path string) {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	if release.status == Fatal {
		return
	}
	release.mu.Lock()
	defer release.mu.Unlock()

	err = os.Rename(internal.InstallPath, path)
	if err == nil {
		internal.InstallPath = path
		release.setInternal(internal)
		return
	}

	// TODO handle cross-device moves
	fmt.Fprintf(os.Stderr, "error moving release '%s' to '%s': %w\n", release, path, err)
}

func (release *Release) Uninstall() {
	internal, err := release.GetInternal()
	if err != nil {
		return
	}

	if release.status == Fatal {
		return
	}
	release.mu.Lock()
	defer release.mu.Unlock()
	release.status = Uninstall
	release.message = "Deleting " + internal.InstallPath
	release.progress = 101
	BroadcastGlobalState()

	// Scary!
	// todo perhaps consider some safeguards to prevent deleting critical directories?
	if err = os.RemoveAll(internal.InstallPath); err != nil {
		fmt.Fprintf(os.Stderr, "error uninstalling release '%s' from '%s': %w\n", release, internal.InstallPath, err)
		release.status = Fatal
		release.err = err
	} else if err = os.Remove(release.getGobPath()); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting gob for release '%s': %w\n", release, err)
		release.status = Fatal
		release.err = err
	}
	BroadcastGlobalState()
}
