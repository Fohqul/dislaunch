package dislaunch

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	cp "github.com/otiai10/copy"
	"github.com/shirou/gopsutil/process"
)

func download(source string, destination io.Writer, progress func(progress uint8)) error {
	response, err := http.Get(source)
	if err != nil {
		return fmt.Errorf("error downloading from '%s': %w", source, err)
	}
	defer response.Body.Close()

	buffer := make([]byte, 32*1024)
	accumulated := 0
	for {
		n, err := response.Body.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}

			return fmt.Errorf("error reading response body: %w", err)
		}
		accumulated += n

		if progress != nil {
			if response.ContentLength >= 0 {
				progress(uint8(float64(accumulated) / float64(response.ContentLength) * 100))
			} else {
				progress(101)
			}
		}

		if _, err := destination.Write(buffer[:n]); err != nil {
			return fmt.Errorf("error writing to destination '%s': %w", destination, err)
		}
	}

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
	mu       sync.Mutex
	status   status
	message  string
	progress uint8 // indeterminate progress when 101
	err      error
	state    atomic.Value
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
	CommandLineArguments string               `json:"command_line_arguments"`
	BetterDiscordEnabled bool                 `json:"bd_enabled"`
	BetterDiscordChannel BetterDiscordChannel `json:"bd_channel"`
}

func (release *Release) String() string {
	switch release {
	case &Stable:
		return "stable"
	case &PTB:
		return "ptb"
	case &Canary:
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
	if err == nil {
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
	// Whilst we would ideally allow `getInternal` to take a shared lock, it may be replaced with an exclusive lock by a call to `setInternal`. See https://pkg.go.dev/github.com/gofrs/flock#Flock.Lock
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
		release.message = "Failed to open internal release data"
		release.err = err
		return nil, nil
	}
	file, err = os.Create(path)
	if err != nil {
		release.status = Fatal
		release.message = "Failed to create internal release data"
		release.err = err
		release.updateState()
		return nil, nil
	}
	if err = gob.NewEncoder(file).Encode(releaseInternal{}); err != nil {
		release.status = Fatal
		release.message = "Failed to encode internal data"
		release.err = err
		release.updateState()
		return nil, nil
	}
	return file, close
}

func (release *Release) setInternal(internal releaseInternal) error {
	file, close := release.openGob()
	if file == nil || close == nil {
		return fmt.Errorf("error opening gob")
	}
	defer close()

	if err := gob.NewEncoder(file).Encode(internal); err != nil {
		release.status = Fatal
		release.message = "Failed to encode internal data"
		release.err = err
		release.updateState()
		return err
	}
	return nil
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
		release.status = Fatal
		release.message = "Failed to decode internal data"
		release.err = err
		return releaseInternal{}, err
	}
	return internal, nil
}

func (release *Release) SetCommandLineArguments(commandLineArguments string) error {
	release.mu.Lock()
	defer release.mu.Unlock()

	internal, err := release.getInternal()
	if err != nil {
		return err
	}

	internal.CommandLineArguments = commandLineArguments
	return release.setInternal(internal)
}

func (release *Release) SetBetterDiscordEnabled(betterDiscordEnabled bool) error {
	release.mu.Lock()
	defer release.mu.Unlock()

	internal, err := release.getInternal()
	if err != nil {
		return err
	}

	internal.BetterDiscordEnabled = betterDiscordEnabled
	if err = release.setInternal(internal); err != nil {
		return err
	}

	if betterDiscordEnabled {
		go release.injectBetterDiscord()
	} else {
		go release.uninjectBetterDiscord()
	}
	return nil
}

func (release *Release) SetBetterDiscordChannel(betterDiscordChannel BetterDiscordChannel) error {
	release.mu.Lock()
	defer release.mu.Unlock()

	internal, err := release.getInternal()
	if err != nil {
		return err
	}

	internal.BetterDiscordChannel = betterDiscordChannel
	if err = release.setInternal(internal); err != nil {
		return err
	}

	go release.injectBetterDiscord()
	return nil
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
		release.status = Fatal
		release.message = "Release is installed, but it reports an unexpected release channel"
		release.err = fmt.Errorf("mismatched release channel: %s", info.ReleaseChannel)
		return "", release.err
	}
	return info.Version, nil
}

type releaseProcessView struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Progress uint8  `json:"progress"`
	Error    string `json:"error"`
}

type ReleaseState struct {
	Internal *releaseInternal    `json:"internal"`
	Version  string              `json:"version"`
	Process  *releaseProcessView `json:"process"`
}

func (release *Release) updateState() {
	state := &ReleaseState{}

	if internal, err := release.getInternal(); err != nil {
		state.Internal = nil
	} else {
		state.Internal = &internal
	}

	if version, err := release.getVersion(); err != nil {
		state.Version = ""
	} else {
		state.Version = version
	}

	state.Process = &releaseProcessView{}
	state.Process.Status = string(release.status)
	state.Process.Message = release.message
	state.Process.Progress = release.progress
	if release.err != nil {
		state.Process.Error = release.err.Error()
	} else {
		state.Process.Error = ""
	}

	release.state.Store(state)
	BroadcastBackendState()
}

func (release *Release) GetState() *ReleaseState {
	state, ok := release.state.Load().(*ReleaseState)

	if !ok {
		fmt.Fprintln(os.Stderr, "error loading release state: ", release) // todo should this be fatal?
		return nil
	}

	if !release.isInstalled() && state.Process.Status != "" {
		return nil
	}

	return state
}

type latestVersion struct {
	Name string `json:"name"`
	// `pub_date` isn't used
}

func (release *Release) CheckForUpdates() {
	release.mu.Lock()
	defer release.mu.Unlock()

	if release.status == Fatal {
		return
	}

	internal, err := release.getInternal()
	if err != nil {
		return
	}

	release.status = UpdateCheck
	release.message = "Checking for updates"
	release.progress = 101
	release.updateState()

	buffer := bytes.NewBuffer(make([]byte, 1024))
	if err := download("https://discord.com/api/"+release.String()+"/updates?platform=linux", buffer, nil); err != nil {
		release.err = fmt.Errorf("error downloading latest version info: %w", err)
		release.updateState()
		return
	}

	var latestVersion latestVersion
	if err := json.NewDecoder(buffer).Decode(&latestVersion); err != nil {
		release.err = fmt.Errorf("error decoding latest version info: %w", err)
		release.updateState()
		return
	}

	internal.LatestVersion = latestVersion.Name
	internal.LastChecked = time.Now()
	release.setInternal(internal)
}

func (release *Release) Install() {
	release.mu.Lock()
	defer release.mu.Unlock()

	installed := release.isInstalled()

	internal, err := release.getInternal()
	if installed && err != nil {
		return
	}

	if installed && release.status == Fatal {
		return
	}

	version, err := release.getVersion()
	if installed && err != nil {
		release.err = fmt.Errorf("error getting installed version: %w", err)
		release.updateState()
		return
	}
	if installed && version == internal.LatestVersion {
		return
	}

	release.status = Install
	release.updateState()

	tarballPath := filepath.Join(GetHomeXDGDirectory("XDG_CACHE_HOME", ".cache"), release.String())
	if installed {
		tarballPath += "-" + internal.LatestVersion
	}
	tarballPath += ".tar.gz"

	if _, err = os.Stat(tarballPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			release.err = fmt.Errorf("error getting stat of tarball download path: %w", err)
			release.updateState()
			return
		}

		downloadPath := tarballPath + ".part"
		if err = os.Remove(downloadPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			release.err = fmt.Errorf("error deleting previous partially downloaded tarball at '%s': %w", downloadPath, err)
			release.updateState()
			return
		}

		file, err := os.OpenFile(downloadPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			release.err = fmt.Errorf("error opening tarball download path: %w", err)
			release.updateState()
			return
		}
		defer file.Close()

		release.message = "Downloading version " + internal.LatestVersion
		if err = download("https://discord.com/api/download/"+release.String()+"?platform=linux&format=tar.gz", file, func(progress uint8) {
			release.progress = progress
			release.updateState()
		}); err != nil {
			release.err = fmt.Errorf("error downloading %s %s: %w", release, internal.LatestVersion, err)
			release.updateState()
			return // todo handle temporary/recoverable errors
		}

		if err = os.Rename(downloadPath, tarballPath); err != nil {
			release.err = fmt.Errorf("error renaming downloaded tarball: %w", err)
			release.updateState()
			return
		}
	}

	installRealpath, err := filepath.EvalSymlinks(internal.InstallPath)
	if err != nil {
		release.err = fmt.Errorf("error getting realpath of install path '%s': %w", internal.InstallPath, err)
		release.updateState()
		return
	}
	processes, err := process.Processes()
	if err != nil {
		release.err = fmt.Errorf("error getting running processes: %w", err)
		release.updateState()
		return
	}
	for _, process := range processes {
		exe, err := process.Exe()
		if err != nil {
			release.err = fmt.Errorf("error getting executable of running process: %w", err)
			release.updateState()
			continue
		}

		exeRealpath, err := filepath.EvalSymlinks(exe)
		if err != nil {
			release.err = fmt.Errorf("error getting realpath of executable '%s': %w", exe, err)
			release.updateState()
			continue
		}

		if strings.HasPrefix(exeRealpath, installRealpath) {
			log.Println("Release '", release, "' is currently running - skipping install")
			return
		}
	}

	// TODO extract archive, delete previous downloads
}

func (release *Release) uninjectBetterDiscord() {
	release.mu.Lock()
	defer release.mu.Unlock()

	if release.status == Fatal {
		return
	}

	_, err := release.getInternal()
	if err != nil {
		return
	}

	// TODO
}

func (release *Release) injectBetterDiscord() {
	release.mu.Lock()
	defer release.mu.Unlock()

	if release.status == Fatal {
		return
	}

	_, err := release.getInternal()
	if err != nil {
		return
	}

	// TODO
}

func (release *Release) Move(path string) {
	release.mu.Lock()
	defer release.mu.Unlock()

	if release.status == Fatal {
		return
	}

	internal, err := release.getInternal()
	if err != nil {
		return
	}

	err = os.Rename(internal.InstallPath, path)
	if err == nil {
		internal.InstallPath = path
		release.setInternal(internal)
		return
	} else if err.(*os.LinkError).Err.(syscall.Errno) == syscall.EXDEV {
		if err = cp.Copy(internal.InstallPath, path, cp.Options{Sync: true, PreserveTimes: true, PreserveOwner: true}); err == nil {
			if err = os.RemoveAll(internal.InstallPath); err != nil {
				release.err = fmt.Errorf("error removing previous install path '%s': %w", internal.InstallPath, err)
				release.updateState()
			}

			internal.InstallPath = path
			release.setInternal(internal)
			return
		}
	}

	release.err = fmt.Errorf("error moving release '%s' to '%s': %w", release, path, err)
	release.updateState()
}

func (release *Release) Uninstall() {
	release.mu.Lock()
	defer release.mu.Unlock()

	if release.status == Fatal {
		return
	}

	internal, err := release.getInternal()
	if err != nil {
		return
	}

	release.status = Uninstall
	release.message = "Deleting " + internal.InstallPath
	release.progress = 101
	release.updateState()

	// Scary!
	// todo perhaps consider some safeguards to prevent deleting critical directories?
	if err = os.RemoveAll(internal.InstallPath); err != nil {
		fmt.Fprintf(os.Stderr, "error uninstalling release '%s' from '%s': %s\n", release, internal.InstallPath, err)
		release.status = Fatal
		release.message = "Failed to uninstall"
		release.err = err
	} else if err = os.Remove(release.getGobPath()); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting gob for release '%s': %s\n", release, err)
		release.status = Fatal
		release.message = "Failed to delete internal data at '" + release.getGobPath() + "'"
		release.err = err
	}
	release.updateState()
}
