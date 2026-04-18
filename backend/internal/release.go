package dislaunch

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json/v2"
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
	"github.com/google/go-github/github"
	"github.com/mholt/archives"
	cp "github.com/otiai10/copy"
	"github.com/shirou/gopsutil/process"
)

func download(ctx context.Context, source string, destination io.Writer, progress func(progress uint8)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("error downloading from '%s': %w", source, err)
	}
	defer response.Body.Close()

	buffer := make([]byte, 32*1024)
	accumulated := 0
	finished := false
	for !finished {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

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

type release struct {
	id                   string
	pathName             string
	desktopEntryFileName string

	mu       sync.Mutex
	ctx      context.Context
	cancel   atomic.Value
	status   status // currently active process
	message  string
	progress uint8 // indeterminate progress when 101
	err      error
	state    atomic.Value
}

var stable, ptb, canary *release

func newRelease(id string, pathName string, desktopEntryFileName string) *release {
	release := &release{id: id, pathName: pathName, desktopEntryFileName: desktopEntryFileName}
	release.resetState(false)

	release.mu.Lock()
	defer release.mu.Unlock()
	release.updateState(false)

	return release
}

func getStable() *release {
	if stable == nil {
		stable = newRelease("stable", "Discord", "discord.desktop")
	}
	return stable
}

func getPtb() *release {
	if ptb == nil {
		ptb = newRelease("ptb", "DiscordPTB", "discord-ptb.desktop")
	}
	return ptb
}

func getCanary() *release {
	if canary == nil {
		canary = newRelease("canary", "DiscordCanary", "discord-canary.desktop")
	}
	return canary
}

type bdChannel string

const (
	bdStable bdChannel = "stable"
	bdCanary bdChannel = "canary"
)

type releaseInternal struct {
	InstallPath          string    `json:"install_path"`
	LastChecked          time.Time `json:"last_checked"`
	LatestVersion        string    `json:"latest_version"`
	CommandLineArguments string    `json:"command_line_arguments"`
	BdEnabled            bool      `json:"bd_enabled"`
	BdChannel            bdChannel `json:"bd_channel"`
	BdInstalledRelease   *int64    `json:"bd_installed_release"`
	BdLatestRelease      *int64    `json:"bd_latest_release"`
}

func (release *release) String() string {
	return release.id
}

func (release *release) getGobPath() string {
	return filepath.Join(getHomeXdgDislaunchDirectory("XDG_STATE_HOME", filepath.Join(".local", "state")), release.id+".gob")
}

func (release *release) isInstalled() bool {
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
func (release *release) openGob(flag int) (*os.File, func()) {
	path := release.getGobPath()
	lock := flock.New(path)
	// Whilst we would ideally allow `getInternal` to take a shared lock, it may be replaced with an exclusive lock by a call to `setInternal`. See https://pkg.go.dev/github.com/gofrs/flock#Flock.Lock
	lock.Lock()
	file, err := os.OpenFile(path, os.O_CREATE|flag, 0600)
	if err != nil {
		release.status = statusFatal
		release.message = "Failed to open internal release data"
		release.err = err
		release.updateState(true)
		return nil, nil
	}
	return file, func() {
		file.Close()
		lock.Unlock()
	}
}

func (release *release) getInternal() (releaseInternal, error) {
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

func (release *release) setInternal(internal *releaseInternal) error {
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
		release.updateState(true)
		return err
	}
	return nil
}

/**
 * Since Discord installs expose their version in `resources/build_info.json`,
 * we can always just read from there to get the installed version without the
 * need to keep track of it ourselves.
 */

func (release *release) getVersion() (string, error) {
	internal, err := release.getInternal()
	if err != nil {
		return "", err
	}

	file, err := os.Open(filepath.Join(internal.InstallPath, release.pathName, "resources", "build_info.json"))
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buildInfo struct {
		Version        string `json:"version"`
		ReleaseChannel string `json:"releaseChannel"`
	}
	if err = json.UnmarshalRead(file, &buildInfo); err != nil {
		return "", err
	}
	if buildInfo.ReleaseChannel != release.id {
		release.status = statusFatal
		release.message = "Release is installed, but it reports an unexpected release channel"
		release.err = fmt.Errorf("mismatched release channel: %s", buildInfo.ReleaseChannel)
		return "", release.err
	}
	return buildInfo.Version, nil
}

func (release *release) takeOver() (*releaseInternal, func()) {
	release.mu.Lock()

	if release.status == statusFatal {
		return nil, nil
	}

	if internal, err := release.getInternal(); err == nil {
		return &internal, func() {
			release.resetState(true)
			release.mu.Unlock()
		}
	}

	return nil, nil
}

type ReleaseState struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Progress uint8  `json:"progress"`
	Error    string `json:"error"`

	Internal *releaseInternal `json:"internal"`
	Version  string           `json:"version"`
}

func (release *release) updateState(broadcast bool) {
	state := &ReleaseState{
		Status:   string(release.status),
		Message:  release.message,
		Progress: release.progress,
	}

	if release.err != nil {
		state.Error = release.err.Error()
	}

	if internal, err := release.getInternal(); err == nil {
		state.Internal = &internal
	}

	if version, err := release.getVersion(); err == nil {
		state.Version = version
	}

	release.state.Store(state)
	if broadcast {
		broadcastBackendState()
	}
}

func (release *release) resetState(broadcast bool) {
	if release.status == statusFatal {
		return
	}

	release.status = statusNone
	release.message = ""
	release.progress = 0
	release.err = nil

	ctx, cancel := context.WithCancel(context.Background())
	release.ctx = ctx
	release.cancel.Store(cancel)

	release.updateState(broadcast)
}

func (release *release) getState() *ReleaseState {
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
		log.Fatalf("%s has a nil `state`\n", release)
		return nil
	}

	state, ok := value.(*ReleaseState)

	if !ok {
		log.Fatalln("error loading release state: ", release)
		return nil
	}

	if !release.isInstalled() && state.Status == string(statusNone) {
		return nil
	}

	return state
}

func (release *release) setCommandLineArguments(commandLineArguments string) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	internal.CommandLineArguments = commandLineArguments
	release.setInternal(internal)
}

func (release *release) setBdEnabled(bdEnabled bool) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	internal.BdEnabled = bdEnabled
	if release.setInternal(internal) != nil {
		return
	}

	release.checkForBdUpdates(internal)
}

func (release *release) setBdChannel(bdChannel bdChannel) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	internal.BdChannel = bdChannel
	if release.setInternal(internal) != nil {
		return
	}

	release.checkForBdUpdates(internal)
}

func (release *release) checkForUpdates() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	release.status = statusUpdateCheck
	release.message = "Checking for updates"
	release.progress = 101
	release.updateState(true)

	var buffer bytes.Buffer
	if err := download(release.ctx, "https://discord.com/api/"+release.id+"/updates?platform=linux", &buffer, nil); err != nil {
		release.err = fmt.Errorf("error downloading latest version info: %w", err)
		release.updateState(true)
		return
	}

	var latestVersion struct {
		Name string `json:"name"`
		// `pub_date` isn't used
	}
	if err := json.UnmarshalRead(&buffer, &latestVersion); err != nil {
		release.err = fmt.Errorf("error decoding latest version info: %w", err)
		release.updateState(true)
		return
	}

	internal.LatestVersion = latestVersion.Name
	internal.LastChecked = time.Now()
	release.setInternal(internal)

	release.checkForBdUpdates(internal)
}

func (release *release) install() {
	// can't use `takeOver` because it fails if not installed
	release.mu.Lock()
	defer release.mu.Unlock()

	installed := release.isInstalled()

	internal, err := release.getInternal()
	if installed && err != nil || release.status == statusFatal {
		return
	}

	defer release.resetState(true)

	// even if installing Discord fails for whatever reason,
	// BetterDiscord should still be updated
	defer func() {
		go release.applyBd()
	}()

	version, err := release.getVersion()
	if installed && err != nil {
		release.err = fmt.Errorf("error getting installed version: %w", err)
		release.updateState(true)
		return
	}
	if installed && version == internal.LatestVersion {
		return
	}

	release.status = statusInstall
	release.updateState(true)

	tarballPath := filepath.Join(getHomeXdgDislaunchDirectory("XDG_CACHE_HOME", ".cache"), release.id)
	if installed {
		tarballPath += "-" + internal.LatestVersion
	}
	tarballPath += ".tar.gz"

	if _, err = os.Stat(tarballPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			release.err = fmt.Errorf("error getting stat of tarball download path: %w", err)
			release.updateState(true)
			return
		}

		downloadPath := tarballPath + ".part"
		if err = os.Remove(downloadPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			release.err = fmt.Errorf("error deleting previous partially downloaded tarball at '%s': %w", downloadPath, err)
			release.updateState(true)
			return
		}

		file, err := os.OpenFile(downloadPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			release.err = fmt.Errorf("error opening tarball download path: %w", err)
			release.updateState(true)
			return
		}
		defer file.Close()

		release.message = "Downloading latest version"
		if err = download(release.ctx, "https://discord.com/api/download/"+release.id+"?platform=linux&format=tar.gz", file, func(progress uint8) {
			release.progress = progress
			release.updateState(true)
		}); err != nil {
			release.err = fmt.Errorf("error downloading %s %s: %w", release, internal.LatestVersion, err)
			release.updateState(true)
			if err := os.Remove(downloadPath); err != nil {
				release.err = fmt.Errorf("error deleting partially downloaded tarball: %w", err)
				release.updateState(true)
			}
			return // todo handle temporary/recoverable errors
		}

		if err = os.Rename(downloadPath, tarballPath); err != nil {
			release.err = fmt.Errorf("error renaming downloaded tarball: %w", err)
			release.updateState(true)
			return
		}
	}

	installPath := internal.InstallPath
	if installPath == "" {
		installPath = getConfiguration().DefaultInstallPath
		if installPath == "" {
			installPath = getDataHome()
		}
	}
	if installed {
		installRealpath, err := filepath.EvalSymlinks(filepath.Join(installPath, release.pathName))
		if err != nil {
			release.err = fmt.Errorf("error getting realpath of install path '%s': %w", filepath.Join(installRealpath, release.pathName), err)
			release.updateState(true)
			return
		}
		processes, err := process.Processes()
		if err != nil {
			release.err = fmt.Errorf("error getting running processes: %w", err)
			release.updateState(true)
			return
		}
		for _, process := range processes {
			exe, err := process.Exe()
			if err != nil {
				release.err = fmt.Errorf("error getting executable of running process: %w", err)
				release.updateState(true)
				continue
			}

			exeRealpath, err := filepath.EvalSymlinks(exe)
			if err != nil {
				release.err = fmt.Errorf("error getting realpath of executable '%s': %w", exe, err)
				release.updateState(true)
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
		release.updateState(true)
		return
	}

	defer func() {
		// Even if extraction failed, that implies a possibly corrupted tarball, so still remove it
		if err = os.Remove(tarballPath); err != nil {
			release.err = fmt.Errorf("error deleting tarball: %w", err)
			release.updateState(true)
		}
	}()

	var desktopEntry bytes.Buffer

	format := archives.CompressedArchive{
		Extraction:  archives.Tar{},
		Compression: archives.Gz{},
	}
	if err = format.Extract(release.ctx, tarball, func(ctx context.Context, info archives.FileInfo) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		release.message = "Extracting " + info.NameInArchive

		if info.IsDir() {
			if err = os.MkdirAll(filepath.Join(installPath, info.NameInArchive), info.Mode().Perm()); err != nil {
				release.err = fmt.Errorf("error creating extracted directory '%s': %w", info.NameInArchive, err)
				release.updateState(true)
				return err
			}
			return nil
		}

		source, err := info.Open()
		if err != nil {
			release.err = fmt.Errorf("error opening extracted file '%s': %w", info.NameInArchive, err)
			release.updateState(true)
			return err
		}
		defer source.Close()

		destination, err := os.OpenFile(filepath.Join(installPath, info.NameInArchive), os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			release.err = fmt.Errorf("error opening destination file '%s': %w", filepath.Join(installPath, info.NameInArchive), err)
			release.updateState(true)
			return err
		}
		defer destination.Close()

		buffer := make([]byte, 32*1024)
		accumulated := 0
		finished := false
		for !finished {
			n, err := source.Read(buffer)
			if err != nil {
				if err != io.EOF {
					release.err = fmt.Errorf("error reading extracted file '%s': %w", info.NameInArchive, err)
					release.updateState(true)
					return err
				}

				finished = true
			}
			accumulated += n
			release.progress = uint8(float64(accumulated) / float64(info.Size()) * 100)
			release.updateState(true)

			if _, err = destination.Write(buffer[:n]); err != nil {
				release.err = fmt.Errorf("error writing extracted file '%s': %w", info.NameInArchive, err)
				release.updateState(true)
				return err
			}

			if info.NameInArchive == filepath.Join(release.pathName, release.desktopEntryFileName) {
				if _, err = desktopEntry.Write(buffer[:n]); err != nil {
					release.err = fmt.Errorf("error writing desktop entry to buffer: %w", err)
					release.updateState(true)
					return err
				}
			}
		}

		return nil
	}); err != nil {
		release.err = fmt.Errorf("error extracting tarball: %w", err)
		release.updateState(true)
		// TODO if already installed, don't delete existing installation - extract first into a temporary dir and, upon finishing extraction without errors, move that into the normal install path
		if err := os.RemoveAll(filepath.Join(installPath, release.pathName)); err != nil {
			release.err = fmt.Errorf("error removing extracted tarball: %w", err)
			release.updateState(true)
		}
		return
	}

	// Even if steps after this fail, still mark release as installed since it's been extracted
	if !installed {
		release.setInternal(&releaseInternal{
			InstallPath: installPath,
			LastChecked: time.Now(), // todo this is silly if we're using a cached tarball and signals that whether a release is installed should not be determined by the presence of its gob/internal data
			BdChannel:   bdStable,
		})
	}

	if desktopEntry.Len() == 0 {
		release.err = fmt.Errorf("error finding desktop file")
		release.updateState(true)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		release.err = fmt.Errorf("error getting home directory: %w", err)
		release.updateState(true)
		return
	}

	oldExec := "Exec=" + filepath.Join("/", "usr", "share", release.desktopEntryFileName[:strings.IndexByte(release.desktopEntryFileName, '.')], release.pathName)
	newExec := "Exec=" + filepath.Join(home, ".local", "bin", "dislaunch") + " " + release.id
	dislaunchDesktopEntry := strings.ReplaceAll(desktopEntry.String(), oldExec, newExec)

	dislaunchDesktopEntryFile, err := os.OpenFile(filepath.Join(getHomeXdgDirectory("XDG_DATA_HOME", filepath.Join(".local", "share")), "applications", release.desktopEntryFileName), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		release.err = fmt.Errorf("error opening .desktop file: %w", err)
		release.updateState(true)
		return
	}
	defer dislaunchDesktopEntryFile.Close()

	release.message = "Writing desktop entry"
	accumulated := 0
	for accumulated < len(dislaunchDesktopEntry) {
		n, err := dislaunchDesktopEntryFile.Write([]byte(dislaunchDesktopEntry)[accumulated:])
		if err != nil {
			release.err = fmt.Errorf("error writing to desktop file: %w", err)
			release.updateState(true)
			return
		}
		accumulated += n
		release.progress = uint8(float64(accumulated) / float64(len(dislaunchDesktopEntry)) * 100)
		release.updateState(true)
	}

}

func (release *release) move(path string) {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	oldPath := filepath.Join(internal.InstallPath, release.pathName)
	newPath := filepath.Join(path, release.pathName)

	release.status = statusMove
	release.message = "Moving to " + newPath
	release.progress = 101
	release.updateState(true)

	err := os.Rename(oldPath, newPath)
	if err == nil {
		internal.InstallPath = path
		release.setInternal(internal)
		return
	}

	if err.(*os.LinkError).Err.(syscall.Errno) != syscall.EXDEV {
		release.err = fmt.Errorf("error moving release '%s' to '%s': %w", release, path, err)
		release.updateState(true)
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
			select {
			case <-release.ctx.Done():
				return true, release.ctx.Err()
			default:
			}
			release.message = "Copying to " + dest
			release.updateState(true)
			return false, nil
		},
	}); err != nil {
		release.err = fmt.Errorf("error copying release '%s' to '%s': %w", release, path, err)
		release.updateState(true)
		if err := os.RemoveAll(newPath); err != nil {
			release.err = fmt.Errorf("error cleaning up new path: %w", err)
			release.updateState(true)
		}
		return
	}

	if err = os.RemoveAll(oldPath); err != nil {
		release.err = fmt.Errorf("error removing previous install path '%s': %w", oldPath, err)
		release.updateState(true)
	}

	internal.InstallPath = path
	release.setInternal(internal)
}

func (release *release) uninstall() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	path := filepath.Join(internal.InstallPath, release.pathName)

	release.status = statusUninstall
	release.message = "Deleting " + path
	release.progress = 101
	release.updateState(true)

	// Scary!
	// todo perhaps consider some safeguards to prevent deleting critical directories?
	if err := os.RemoveAll(path); err != nil {
		fmt.Fprintf(os.Stderr, "error uninstalling release '%s' from '%s': %s\n", release, internal.InstallPath, err)
		release.status = statusFatal
		release.message = "Failed to uninstall"
		release.err = err
		release.updateState(true)
	}

	if err := os.Remove(filepath.Join(getHomeXdgDirectory("XDG_DATA_HOME", filepath.Join(".local", "share")), "applications", release.desktopEntryFileName)); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting desktop entry for release '%s': %s\n", release, err)
		release.status = statusFatal
		release.message = "Failed to delete desktop entry"
		release.err = err
		release.updateState(true)
	}

	if err := os.Remove(release.getGobPath()); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting gob for release '%s': %s\n", release, err)
		release.status = statusFatal
		release.message = "Failed to delete internal data at '" + release.getGobPath() + "'"
		release.err = err
		release.updateState(true)
	}
	release.updateState(true)
}

func (release *release) checkForBdUpdates(internal *releaseInternal) error {
	if !internal.BdEnabled {
		return nil
	}

	client := github.NewClient(nil)

	switch internal.BdChannel {
	case bdStable:
		bdRelease, _, err := client.Repositories.GetLatestRelease(release.ctx, "BetterDiscord", "BetterDiscord")
		if err != nil {
			release.err = fmt.Errorf("error getting latest BetterDiscord release: %w", err)
			release.updateState(true)
			return err
		}
		internal.BdLatestRelease = bdRelease.ID
	case bdCanary:
		releases, _, err := client.Repositories.ListReleases(release.ctx, "BetterDiscord", "BetterDiscord", &github.ListOptions{Page: 1, PerPage: 1})
		if err != nil {
			release.err = fmt.Errorf("error getting BetterDiscord releases: %w", err)
			release.updateState(true)
			return err
		}
		internal.BdLatestRelease = releases[0].ID
	default:
		release.err = fmt.Errorf("invalid BetterDiscord release channel: %s", internal.BdChannel)
		release.updateState(true)
		return release.err
	}

	return release.setInternal(internal)
}

func (release *release) applyBd() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	release.status = statusBdInjection
	release.updateState(true)

	version, err := release.getVersion()
	if err != nil {
		return
	}

	release.status = statusBdInjection

	path := filepath.Join(getHomeXdgDirectory("XDG_CONFIG_HOME", ".config"), strings.ToLower(release.pathName), version, "modules", "discord_desktop_core")

	if internal.BdEnabled {
		if internal.BdLatestRelease == nil && release.checkForBdUpdates(internal) != nil {
			return
		}

		if internal.BdInstalledRelease == nil || *internal.BdInstalledRelease != *internal.BdLatestRelease {
			client := github.NewClient(nil)
			bdRelease, _, err := client.Repositories.GetRelease(release.ctx, "BetterDiscord", "BetterDiscord", *internal.BdLatestRelease)
			if err != nil {
				release.err = fmt.Errorf("error getting latest BetterDiscord release: %w", err)
				release.updateState(true)
				return
			}

			for _, asset := range bdRelease.Assets {
				if *asset.Name != "betterdiscord.asar" {
					continue
				}

				if err = os.MkdirAll(path, 0755); err != nil {
					release.err = fmt.Errorf("error creating '%s': %w", path, err)
					release.updateState(true)
					return
				}

				asarPath := filepath.Join(path, "betterdiscord.asar")

				asar, err := os.OpenFile(asarPath, os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					release.err = fmt.Errorf("error opening '%s': %w", asarPath, err)
					release.updateState(true)
					return
				}

				release.message = "Downloading BetterDiscord"
				release.updateState(true)

				if err = download(release.ctx, *asset.BrowserDownloadURL, asar, func(progress uint8) {
					release.progress = progress
					release.updateState(true)
				}); err != nil {
					release.err = fmt.Errorf("error downloading BetterDiscord: %w", err)
					release.updateState(true)
					return
				}

				internal.BdInstalledRelease = internal.BdLatestRelease
				release.setInternal(internal)

				break
			}
		}
	} else {
		release.message = "Removing BetterDiscord"

		if err = os.Remove(filepath.Join(path, "betterdiscord.asar")); err != nil && !errors.Is(err, os.ErrNotExist) {
			release.err = fmt.Errorf("error deleting BetterDiscord: %w", err)
			release.updateState(true)
		}

		internal.BdInstalledRelease = nil
		internal.BdLatestRelease = nil
		release.setInternal(internal)
	}
	release.updateState(true)

	indexJsPath := filepath.Join(path, "index.js")
	indexJs, err := os.OpenFile(indexJsPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		release.err = fmt.Errorf("error opening '%s': %w", indexJsPath, err)
		release.updateState(true)
		return
	}

	var content string
	if internal.BdEnabled {
		content = "require(\"./betterdiscord.asar\");\nmodule.exports = require(\"./core.asar\");"
		release.message = "Injecting BetterDiscord"
		release.updateState(true)
	} else {
		content = "module.exports = require('./core.asar');"
	}

	accumulated := 0
	for accumulated < len(content) {
		n, err := indexJs.WriteString(content)
		if err != nil {
			release.err = fmt.Errorf("error writing to '%s': %w", indexJsPath, err)
			release.updateState(true)
			return
		}
		accumulated += n
	}
}
