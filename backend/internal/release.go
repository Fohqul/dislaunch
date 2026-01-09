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
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"
)

func download(source string, destination string, progress func(progress uint8)) error {
	// TODO

	return nil
}

type status string

const (
	None                   status = ""
	Download               status = "download"
	Install                status = "install"
	UpdateCheck            status = "update_check"
	BetterDiscordInjection status = "bd_injection"
	Move                   status = "move"
	Uninstall              status = "uninstall"
	Fatal                  status = "fatal"
)

type Release struct {
	mu             sync.Mutex
	status         status
	progressWindow bool
	message        string
	progress       uint8 // indeterminate progress when 101
	err            error
	state          atomic.Value
}

var Stable, PTB, Canary Release

type BetterDiscordChannel string

const (
	BDStable BetterDiscordChannel = "stable"
	BDCanary BetterDiscordChannel = "canary"
)

type releaseInternal struct {
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

func (release *Release) isInstalled() bool {
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

func (release *Release) openGob() (*os.File, func()) {
	path := release.getGobPath()
	lock := flock.New(path)
	// Whilst we would ideally allow `getInternal` to take a shared lock, it may be replaced with an exclusive lock by a call to `getInternal`. See https://pkg.go.dev/github.com/gofrs/flock#Flock.Lock
	lock.Lock()
	file, err := os.Open(path)
	close := func() {
		file.Close()
		lock.Unlock()
	}
	if err == nil {
		return file, close
	}
	if !errors.Is(err, fs.ErrNotExist) {
		release.status = Fatal
		release.err = err
		return nil, nil
	}
	file, err = os.Create(path)
	if err != nil {
		release.status = Fatal
		release.err = err
		release.updateState()
		return nil, nil
	}
	if err = gob.NewEncoder(file).Encode(releaseInternal{}); err != nil {
		release.status = Fatal
		release.err = err
		release.updateState()
		return nil, nil
	}
	return file, close
}

func (release *Release) setInternal(internal releaseInternal) {
	file, close := release.openGob()
	if file == nil || close == nil {
		return
	}
	defer close()

	if err := gob.NewEncoder(file).Encode(internal); err != nil {
		release.status = Fatal
		release.err = err
		release.updateState()
	}
}

func (release *Release) getInternal() (releaseInternal, error) {
	if !release.isInstalled() {
		return releaseInternal{}, fmt.Errorf("release '%s' is not installed", release)
	}

	file, close := release.openGob()
	if file == nil || close == nil {
		return releaseInternal{}, fmt.Errorf("error opening gob")
	}
	defer close()

	var internal releaseInternal
	if err := gob.NewDecoder(file).Decode(&internal); err != nil {
		release.mu.Lock()
		defer release.mu.Unlock()
		release.status = Fatal
		release.err = err
		return releaseInternal{}, err
	}
	return internal, nil
}

func (release *Release) setLastChecked(lastChecked time.Time) {
	internal, err := release.getInternal()
	if err != nil {
		return
	}

	internal.LastChecked = lastChecked
	release.setInternal(internal)
}

func (release *Release) SetBetterDiscordEnabled(betterDiscordEnabled bool) {
	internal, err := release.getInternal()
	if err != nil {
		return
	}

	internal.BetterDiscordEnabled = betterDiscordEnabled
	release.setInternal(internal)
}

func (release *Release) SetBetterDiscordChannel(betterDiscordChannel BetterDiscordChannel) {
	internal, err := release.getInternal()
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

func (release *Release) getVersion() (string, error) {
	internal, err := release.getInternal()
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

type releaseProcessView struct {
	Status         string `json:"status"`
	ProgressWindow bool   `json:"progress_window"`
	Message        string `json:"message"`
	Progress       uint8  `json:"progress"`
	Error          string `json:"error"`
}

type ReleaseState struct {
	Internal *releaseInternal    `json:"internal"`
	Version  string              `json:"version"`
	Process  *releaseProcessView `json:"process"`
}

func (release *Release) updateState() {
	var state *ReleaseState

	if internal, err := release.getInternal(); err != nil {
		state.Internal = &internal
	} else {
		state.Internal = nil
	}

	if version, err := release.getVersion(); err != nil {
		state.Version = version
	} else {
		state.Version = ""
	}

	state.Process.Status = string(release.status)
	state.Process.ProgressWindow = release.progressWindow
	state.Process.Message = release.message
	state.Process.Progress = release.progress
	if release.err != nil {
		state.Process.Error = release.err.Error()
	} else {
		state.Process.Error = ""
	}

	release.state.Store(state)
	BroadcastGlobalState()
}

func (release *Release) GetState() *ReleaseState {
	if !release.isInstalled() {
		return nil
	}

	return release.state.Load().(*ReleaseState)
}

func (release *Release) CheckForUpdates() {
	if release.status == Fatal {
		return
	}

	internal, err := release.getInternal()
	if err != nil {
		return
	}

	release.mu.Lock()
	defer release.mu.Unlock()

	release.status = UpdateCheck
	release.message = "Checking for updates"
	release.progress = 101
	release.updateState()

	url := "https://discord.com/api/" + release.String() + "/updates?platform=linux"
	// TODO
}

func (release *Release) Install() {
	internal, err := release.getInternal()
	if release.isInstalled() && err != nil {
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
	if release.status == Fatal {
		return
	}

	internal, err := release.getInternal()
	if err != nil {
		return
	}

	release.mu.Lock()
	defer release.mu.Unlock()

	// TODO
}

func (release *Release) Move(path string) {
	if release.status == Fatal {
		return
	}

	internal, err := release.getInternal()
	if err != nil {
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
	if release.status == Fatal {
		return
	}

	internal, err := release.getInternal()
	if err != nil {
		return
	}

	release.mu.Lock()
	defer release.mu.Unlock()
	release.status = Uninstall
	release.message = "Deleting " + internal.InstallPath
	release.progress = 101
	release.updateState()

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
	release.updateState()
}
