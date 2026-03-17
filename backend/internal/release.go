package dislaunch

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/mholt/archives"
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
	finished := false
	for !finished {
		n, err := response.Body.Read(buffer)
		if err != nil {
			if err != io.EOF {
				return fmt.Errorf("error reading response body: %w", err)
			}

			finished = true
		}
		accumulated += n

		if progress != nil {
			if response.ContentLength >= 0 {
				progress(uint8(float64(accumulated) / float64(response.ContentLength) * 100))
			} else {
				progress(101)
			}
		}

		if _, err = destination.Write(buffer[:n]); err != nil {
			return fmt.Errorf("error writing to destination '%s': %w", destination, err)
		}
	}

	return nil
}

type status string

const (
	statusNone        status = ""
	statusDownload    status = "download"
	statusInstall     status = "install"
	statusUpdateCheck status = "update_check"
	statusBdInjection status = "bd_injection"
	statusMove        status = "move"
	statusUninstall   status = "uninstall"
	// A fatal status indicates that, when a release is installed, something has gone seriously wrong and
	// the application has reached a state it never should have. Processes should return immediately when
	// the state becomes fatal so as to prevent further damage being done or further errors occurring.
	// However, a fatal status is expected when the release is not installed.
	statusFatal status = "fatal"
)

// A "process" is essentially a method of `Release` which is
// publicly accessible, such as `CheckForUpdates`, `Move` and
// `Install`, along with the state associated with that,
// such as `status`, `message`, `progress` and `err`.

type Release struct {
	mu       sync.Mutex
	status   status // currently active process
	message  string
	progress uint8 // indeterminate progress when 101
	err      error
	state    atomic.Value
}

var Stable, PTB, Canary Release

type BdChannel string

const (
	BdStable BdChannel = "stable"
	BdCanary BdChannel = "canary"
)

type releaseInternal struct {
	InstallPath          string    `json:"install_path"`
	LastChecked          time.Time `json:"last_checked"`
	LatestVersion        string    `json:"latest_version"`
	CommandLineArguments string    `json:"command_line_arguments"`
	BdEnabled            bool      `json:"bd_enabled"`
	BdChannel            BdChannel `json:"bd_channel"`
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

func (release *Release) pathName() string {
	switch release {
	case &Stable:
		return "Discord"
	case &PTB:
		return "DiscordPTB"
	case &Canary:
		return "DiscordCanary"
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

// Any errors in dealing with internal release data
// (e.g. opening the gob, encoding/decoding) are always
// considered fatal. So that their callers don't all need
// to handle updating the release's state (`release.status
// = Fatal`, `release.err = err` etc.), the helpers `openGob`,
// `setInternal`, `getInternal` and `getVersion` all do this
// automatically. Therefore, their callers must not only hold
// the lock, but also return immediately if these helpers
// return an error, as that means the status is fatal (unless
// it is expected to be, such as when the release isn't
// installed.)

// `nil, nil` return value means an error occurred
func (release *Release) openGob(flag int) (*os.File, func()) {
	path := release.getGobPath()
	lock := flock.New(path)
	// Whilst we would ideally allow `getInternal` to take a shared lock, it may be replaced with an exclusive lock by a call to `setInternal`. See https://pkg.go.dev/github.com/gofrs/flock#Flock.Lock
	lock.Lock()
	file, err := os.OpenFile(path, os.O_CREATE|flag, 0600)
	if err != nil {
		release.status = statusFatal
		release.message = "Failed to open internal release data"
		release.err = err
		release.updateState()
		return nil, nil
	}
	return file, func() {
		file.Close()
		lock.Unlock()
	}
}

func (release *Release) setInternal(internal *releaseInternal) error {
	if internal == nil {
		err := fmt.Errorf("internal is nil")
		fmt.Fprintln(os.Stderr, err)
		return err
	}

	file, close := release.openGob(os.O_WRONLY)
	if file == nil || close == nil {
		return fmt.Errorf("error opening gob")
	}
	defer close()

	if err := gob.NewEncoder(file).Encode(internal); err != nil {
		release.status = statusFatal
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

	file, close := release.openGob(os.O_RDONLY)
	if file == nil || close == nil {
		return releaseInternal{}, fmt.Errorf("error opening gob")
	}
	defer close()

	var internal releaseInternal
	if err := gob.NewDecoder(file).Decode(&internal); err != nil {
		release.status = statusFatal
		release.message = "Failed to decode internal data"
		release.err = err
		return releaseInternal{}, err
	}
	return internal, nil
}

/**
 * Since Discord installs expose their version in `resources/build_info.json`,
 * we can always just read from there to get the installed version without the
 * need to keep track of it ourselves.
 */

func (release *Release) getVersion() (string, error) {
	internal, err := release.getInternal()
	if err != nil {
		return "", err
	}

	file, err := os.Open(filepath.Join(internal.InstallPath, release.pathName(), "resources", "build_info.json"))
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buildInfo struct {
		Version        string `json:"version"`
		ReleaseChannel string `json:"releaseChannel"`
	}
	if err = json.NewDecoder(file).Decode(&buildInfo); err != nil {
		return "", err
	}
	if buildInfo.ReleaseChannel != release.String() {
		release.status = statusFatal
		release.message = "Release is installed, but it reports an unexpected release channel"
		release.err = fmt.Errorf("mismatched release channel: %s", buildInfo.ReleaseChannel)
		return "", release.err
	}
	return buildInfo.Version, nil
}

func (release *Release) takeOver() (*releaseInternal, func()) {
	release.mu.Lock()

	if release.status == statusFatal {
		return nil, nil
	}

	if internal, err := release.getInternal(); err == nil {
		return &internal, func() {
			release.resetState()
			release.mu.Unlock()
		}
	}

	return nil, nil
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

	if internal, err := release.getInternal(); err == nil {
		state.Internal = &internal
	}

	if version, err := release.getVersion(); err == nil {
		state.Version = version
	}

	state.Process = &releaseProcessView{
		Status:   string(release.status),
		Message:  release.message,
		Progress: release.progress,
	}
	if release.err != nil {
		state.Process.Error = release.err.Error()
	}

	release.state.Store(state)
	BroadcastBackendState()
}

func (release *Release) resetState() {
	if release.status == statusFatal {
		return
	}

	release.status = statusNone
	release.message = ""
	release.progress = 0
	release.err = nil
	release.updateState()
}

func (release *Release) GetState() *ReleaseState {
	// `GetState` returns the atomic value `release.state`
	// because if it composed/constructed the state using
	// `getInternal` and `getVersion`, it would require the
	// lock. But in many cases, that lock is held by an
	// active process, which prevents progress reports from
	// being broadcast. So, processes use the `updateState`
	// helper (whose callers already own the lock) to mutate
	// `release.state`, which `GetState` then reads from.

	value := release.state.Load()

	if value == nil {
		go func() {
			// TODO there should be a better mechanism than this for automatically loading in the value without the need for a process
			release.mu.Lock()
			defer release.mu.Unlock()
			release.updateState()
		}()
		return nil
	}

	state, ok := value.(*ReleaseState)

	if !ok {
		fmt.Fprintln(os.Stderr, "error loading release state: ", release) // todo should this be fatal?
		return nil
	}

	if !release.isInstalled() && state.Process.Status == string(statusNone) {
		return nil
	}

	return state
}

func (release *Release) SetCommandLineArguments(commandLineArguments string) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	internal.CommandLineArguments = commandLineArguments
	release.setInternal(internal)
}

func (release *Release) SetBdEnabled(bdEnabled bool) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	internal.BdEnabled = bdEnabled
	if release.setInternal(internal) != nil {
		return
	}

	if bdEnabled {
		go release.injectBd()
	} else {
		go release.uninjectBd()
	}
}

func (release *Release) SetBdChannel(bdChannel BdChannel) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	internal.BdChannel = bdChannel
	if release.setInternal(internal) != nil {
		return
	}

	if internal.BdEnabled {
		go release.injectBd()
	}
}

func (release *Release) CheckForUpdates() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	release.status = statusUpdateCheck
	release.message = "Checking for updates"
	release.progress = 101
	release.updateState()

	var buffer bytes.Buffer
	if err := download("https://discord.com/api/"+release.String()+"/updates?platform=linux", &buffer, nil); err != nil {
		release.err = fmt.Errorf("error downloading latest version info: %w", err)
		release.updateState()
		return
	}

	var latestVersion struct {
		Name string `json:"name"`
		// `pub_date` isn't used
	}
	if err := json.NewDecoder(&buffer).Decode(&latestVersion); err != nil {
		release.err = fmt.Errorf("error decoding latest version info: %w", err)
		release.updateState()
		return
	}

	internal.LatestVersion = latestVersion.Name
	internal.LastChecked = time.Now()
	release.setInternal(internal)
}

func (release *Release) Install() {
	// can't use `takeOver` because it fails if not installed
	release.mu.Lock()
	defer release.mu.Unlock()

	installed := release.isInstalled()

	internal, err := release.getInternal()
	if installed && err != nil {
		return
	}

	if installed && release.status == statusFatal {
		return
	}

	defer release.resetState()

	version, err := release.getVersion()
	if installed && err != nil {
		release.err = fmt.Errorf("error getting installed version: %w", err)
		release.updateState()
		return
	}
	if installed && version == internal.LatestVersion {
		return
	}

	release.status = statusInstall
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

		if internal.LatestVersion != "" {
			release.message = "Downloading version " + internal.LatestVersion
		} else {
			release.message = "Downloading latest version"
		}
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

	installPath := internal.InstallPath
	if installPath == "" {
		installPath = GetConfiguration().DefaultInstallPath
		if installPath == "" {
			installPath = GetDataHome()
		}
	}
	if installed {
		installRealpath, err := filepath.EvalSymlinks(filepath.Join(installPath, release.pathName()))
		if err != nil {
			release.err = fmt.Errorf("error getting realpath of install path '%s': %w", filepath.Join(installRealpath, release.pathName()), err)
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
	}

	// TODO delete previous downloads
	tarball, err := os.Open(tarballPath)
	if err != nil {
		release.err = fmt.Errorf("error opening tarball: %w", err)
		release.updateState()
		return
	}

	format := archives.CompressedArchive{
		Extraction:  archives.Tar{},
		Compression: archives.Gz{},
	}
	if err = format.Extract(context.Background(), tarball, func(_ context.Context, info archives.FileInfo) error {
		release.message = "Extracting " + info.NameInArchive

		if info.IsDir() {
			if err = os.MkdirAll(filepath.Join(installPath, info.NameInArchive), info.Mode().Perm()); err != nil {
				release.err = fmt.Errorf("error creating extracted directory '%s': %w", info.NameInArchive, err)
				release.updateState()
				return err
			}
		} else {
			source, err := info.Open()
			if err != nil {
				release.err = fmt.Errorf("error opening extracted file '%s': %w", info.NameInArchive, err)
				release.updateState()
				return err
			}

			destination, err := os.OpenFile(filepath.Join(installPath, info.NameInArchive), os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				release.err = fmt.Errorf("error opening destination file '%s': %w", filepath.Join(installPath, info.NameInArchive), err)
				release.updateState()
				return err
			}

			buffer := make([]byte, 32*1024)
			accumulated := 0
			finished := false
			for !finished {
				n, err := source.Read(buffer)
				if err != nil {
					if err != io.EOF {
						release.err = fmt.Errorf("error reading extracted file '%s': %w", info.NameInArchive, err)
						release.updateState()
						return err
					}

					finished = true
				}
				accumulated += n
				release.progress = uint8(float64(accumulated) / float64(info.Size()) * 100)
				release.updateState()

				if _, err = destination.Write(buffer[:n]); err != nil {
					release.err = fmt.Errorf("error writing extracted file '%s': %w", info.NameInArchive, err)
					release.updateState()
					return err
				}
			}
		}

		return nil
	}); err != nil {
		release.err = fmt.Errorf("error extracting tarball: %w", err)
		release.updateState()
	}

	// Even if extraction failed, that implies a possibly corrupted tarball, so still remove it
	if err = os.Remove(tarballPath); err != nil {
		release.err = fmt.Errorf("error deleting tarball: %w", err)
		release.updateState()
	}

	if !installed {
		release.setInternal(&releaseInternal{
			InstallPath: installPath,
			LastChecked: time.Now(), // todo this is silly if we're using a cached tarball and signals that whether a release is installed should not be determined by the presence of its gob/internal data
			BdChannel:   BdStable,
		})
	}
}

func (release *Release) Move(path string) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	oldPath := filepath.Join(internal.InstallPath, release.pathName())
	newPath := filepath.Join(path, release.pathName())

	release.status = statusMove
	release.message = "Moving to " + newPath
	release.progress = 101
	release.updateState()

	err := os.Rename(oldPath, newPath)
	if err == nil {
		internal.InstallPath = path
		release.setInternal(internal)
		return
	}

	if err.(*os.LinkError).Err.(syscall.Errno) != syscall.EXDEV {
		release.err = fmt.Errorf("error moving release '%s' to '%s': %w", release, path, err)
		release.updateState()
		return
	}

	if err = cp.Copy(oldPath, newPath, cp.Options{
		Sync:          true,
		PreserveTimes: true,
		PreserveOwner: true,
		// HACK: Because we need to report status to the frontend,
		// we need to run a callback for each file/directory.
		// The closest this package gives us is this `Skip`
		// callback which obviously isn't meant for this,
		// but if it works, it works.
		Skip: func(_ os.FileInfo, _ string, dest string) (bool, error) {
			release.message = "Copying to " + dest
			release.updateState()
			// Don't skip, literally the only point of this is status reporting
			return false, nil
		},
	}); err != nil {
		release.err = fmt.Errorf("error copying release '%s' to '%s': %w", release, path, err)
		release.updateState()
		return
	}

	if err = os.RemoveAll(oldPath); err != nil {
		release.err = fmt.Errorf("error removing previous install path '%s': %w", oldPath, err)
		release.updateState()
	}

	internal.InstallPath = path
	release.setInternal(internal)
}

func (release *Release) Uninstall() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	path := filepath.Join(internal.InstallPath, release.pathName())

	release.status = statusUninstall
	release.message = "Deleting " + path
	release.progress = 101
	release.updateState()

	// Scary!
	// todo perhaps consider some safeguards to prevent deleting critical directories?
	if err := os.RemoveAll(path); err != nil {
		fmt.Fprintf(os.Stderr, "error uninstalling release '%s' from '%s': %s\n", release, internal.InstallPath, err)
		release.status = statusFatal
		release.message = "Failed to uninstall"
		release.err = err
	} else if err = os.Remove(release.getGobPath()); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting gob for release '%s': %s\n", release, err)
		release.status = statusFatal
		release.message = "Failed to delete internal data at '" + release.getGobPath() + "'"
		release.err = err
	}
	release.updateState()
}

func (release *Release) injectBd() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	// TODO
}

func (release *Release) uninjectBd() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	// TODO
}
