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
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

func versionArrayToString(version [3]int) string {
	return fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])
}

type status string

const (
	statusNone        status = ""
	statusInstall     status = "install"
	statusUpdateCheck status = "update_check"
	statusBdInjection status = "bd_injection"
	statusUninstall   status = "uninstall"
	// A fatal status indicates that, when a release is installed, something has gone seriously wrong and
	// the application has reached a state it never should have. Processes should return immediately when
	// the state becomes fatal so as to prevent further damage being done or further errors occurring.
	statusFatal status = "fatal"
)

type bdChannel string

const (
	bdStable bdChannel = "stable"
	bdCanary bdChannel = "canary"
)

type releaseInternal struct {
	InstalledVersion     string    `json:"installed_version"`
	LastChecked          time.Time `json:"last_checked"`
	LatestVersion        string    `json:"latest_version"`
	CommandLineArguments string    `json:"command_line_arguments"`
	BdEnabled            bool      `json:"bd_enabled"`
	BdChannel            bdChannel `json:"bd_channel"`
	BdInstalledRelease   *int64    `json:"bd_installed_release"`
	BdLatestRelease      *int64    `json:"bd_latest_release"`
}

// A "process" is essentially a method of `release` which is
// publicly accessible, such as `checkForUpdates`, `move` and
// `install`, along with the state associated with that,
// such as `status`, `message`, `progress` and `err`.

type release struct {
	id                   string
	pathName             string
	gobPath              string
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

type releaseState struct {
	Status   status `json:"status"`
	Message  string `json:"message"`
	Progress uint8  `json:"progress"`
	Error    string `json:"error"`

	Internal *releaseInternal `json:"internal"`
}

var stableOnce, ptbOnce, canaryOnce sync.Once

var stable, ptb, canary *release

func newRelease(id string, pathName string, desktopEntryFileName string) *release {
	release := &release{
		id:                   id,
		pathName:             pathName,
		gobPath:              filepath.Join(getHomeXdgDislaunchDirectory("XDG_STATE_HOME", filepath.Join(".local", "state")), id+".gob"),
		desktopEntryFileName: desktopEntryFileName,
	}

	release.mu.Lock()
	defer release.mu.Unlock()

	release.reset(nil, false)

	return release
}

func getStable() *release {
	stableOnce.Do(func() {
		stable = newRelease("stable", "Discord", "discord.desktop")
	})
	return stable
}

func getPtb() *release {
	ptbOnce.Do(func() {
		ptb = newRelease("ptb", "DiscordPTB", "discord-ptb.desktop")
	})
	return ptb
}

func getCanary() *release {
	canaryOnce.Do(func() {
		canary = newRelease("canary", "DiscordCanary", "discord-canary.desktop")
	})
	return canary
}

func (release *release) String() string {
	return release.id
}

func (release *release) getInstallPath(version string) (string, error) {
	if version == "" {
		return "", nil
	}

	config, err := os.UserConfigDir()
	if err != nil {
		release.status = statusFatal
		release.err = fmt.Errorf("error getting user config directory: %w", err)
		release.flush(nil, true)
		return "", release.err
	}

	return filepath.Join(config, strings.ToLower(release.pathName), "app-"+version), nil
}

// Any errors in dealing with internal release data
// (e.g. opening the gob, encoding/decoding) are always
// considered fatal. So that their callers don't all need
// to handle updating the release's state (`release.status
// = statusFatal`, `release.err = err` etc.), the helpers
// `openGob`, `setInternal`, `getInternal` and `getVersion`
// all do this automatically. Therefore, their callers must
// not only hold the lock, but also return immediately if
// these helpers return an error, as that means the status
// is fatal.

// `nil, nil` return value means an error occurred
func (release *release) openGob(flag int) (*os.File, func()) {
	lock := flock.New(release.gobPath)
	// Whilst we would ideally allow `getInternal` to take a shared lock, it may be replaced with an exclusive lock by a call to `setInternal`. See https://pkg.go.dev/github.com/gofrs/flock#Flock.Lock
	lock.Lock()
	file, err := os.OpenFile(release.gobPath, os.O_CREATE|flag, 0600)
	if err != nil {
		release.status = statusFatal
		release.err = fmt.Errorf("error opening internal data for release '%s': %w", release, err)
		release.flush(nil, true)
		return nil, nil
	}
	return file, func() {
		file.Close()
		lock.Unlock()
	}
}

func (release *release) getInternal() (releaseInternal, error) {
	file, close := release.openGob(os.O_RDONLY)
	if file == nil || close == nil {
		return releaseInternal{}, fmt.Errorf("error opening gob")
	}
	defer close()

	var internal releaseInternal
	if err := gob.NewDecoder(file).Decode(&internal); err != nil {
		if err == io.EOF {
			return releaseInternal{BdChannel: bdStable}, nil
		}

		release.status = statusFatal
		release.err = fmt.Errorf("error decoding internal data for release '%s': %w", release, err)
		return releaseInternal{}, release.err
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
		release.err = fmt.Errorf("error encoding internal data for release '%s': %w", release, err)
		release.flush(nil, true)
		return release.err
	}
	return nil
}

func (release *release) takeOver() (*releaseInternal, func()) {
	release.mu.Lock()

	if release.status == statusFatal {
		return nil, nil
	}

	if internal, err := release.getInternal(); err == nil {
		return &internal, func() {
			release.setInternal(&internal)
			release.flush(&internal, true)
			release.reset(&internal, true)
			release.mu.Unlock()
		}
	}

	return nil, nil
}

func (release *release) flush(internal *releaseInternal, broadcast bool) {
	state := &releaseState{
		Status:   release.status,
		Message:  release.message,
		Progress: release.progress,
	}

	if release.err != nil {
		slog.Error(release.err.Error())
		state.Error = release.err.Error()
	}

	if internal != nil {
		state.Internal = internal
	} else if internal, err := release.getInternal(); err == nil {
		state.Internal = &internal
	}

	release.state.Store(state)
	if broadcast {
		broadcastBackendState()
	}
}

func (release *release) reset(internal *releaseInternal, broadcast bool) {
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

	release.flush(internal, broadcast)
}

func (release *release) getState() *releaseState {
	// `getState` returns the atomic value `release.state`
	// because if it composed/constructed the state using
	// `getInternal` and `getVersion`, it would require the
	// lock. But in many cases, that lock is held by an
	// active process, which prevents progress reports from
	// being broadcast. So, processes use the `updateState`
	// helper (whose callers already own the lock) to mutate
	// `release.state`, which `getState` then reads from.

	value := release.state.Load()

	if value == nil {
		log.Fatalf("%s has a nil `state`\n", release)
		return nil
	}

	state, ok := value.(*releaseState)

	if !ok {
		log.Fatalln("error loading release state: ", release)
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

type manifestPackageVersion struct {
	HostVersion   [3]int `json:"host_version"`
	ModuleVersion *int   `json:"module_version"`
	PackageSha256 string `json:"package_sha256"`
	Url           string `json:"url"`
}

type manifestPackage struct {
	Full   manifestPackageVersion   `json:"full"`
	Deltas []manifestPackageVersion `json:"deltas"`
}

type manifest struct {
	manifestPackage
	Modules         map[string]manifestPackage `json:"modules"`
	RequiredModules []string                   `json:"required_modules"`
	MetadataVersion *int                       `json:"metadata_version"`
	RequiredUpdate  bool                       `json:"required_update"`
}

func (release *release) getLatestManifest(internal *releaseInternal) (manifest, error) {
	var buffer bytes.Buffer

	if err := download(release.ctx, "https://updates.discord.com/distributions/app/manifests/latest?platform=linux&arch=x64&channel="+release.id, &buffer, func(progress uint8) {
		release.progress = progress
		release.flush(internal, true)
	}); err != nil {
		release.err = fmt.Errorf("error downloading latest manifest for '%s': %w", release, err)
		release.flush(internal, true)
		return manifest{}, release.err
	}

	var latestManifest manifest
	if err := json.UnmarshalRead(&buffer, &latestManifest); err != nil {
		release.err = fmt.Errorf("error decoding latest manifest for '%s': %w", release, err)
		release.flush(internal, true)
		return manifest{}, release.err
	}

	internal.LatestVersion = versionArrayToString(latestManifest.Full.HostVersion)
	internal.LastChecked = time.Now()
	if err := release.setInternal(internal); err != nil {
		return manifest{}, err
	}

	return latestManifest, nil
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
	release.flush(internal, true)

	if _, err := release.getLatestManifest(internal); err != nil {
		return
	}

	release.checkForBdUpdates(internal)
}

func (release *release) downloadDistro(internal *releaseInternal, manifest manifestPackageVersion, destination string) error {
	if _, err := os.Stat(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("error getting stat of path '%s': %w", destination, err)
	}

	downloadPath := destination + ".part"
	if err := os.Remove(downloadPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("error deleting previously partially downloaded distro at '%s': %w", downloadPath, err)
	}

	file, err := os.OpenFile(downloadPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("error opening distro download path at '%s': %w", downloadPath, err)
	}
	defer file.Close()

	if err = download(release.ctx, manifest.Url, file, func(progress uint8) {
		release.progress = progress
		release.flush(internal, true)
	}); err != nil {
		release.err = fmt.Errorf("error downloading '%s': %w", manifest.Url, err)
		release.flush(internal, true)
		if err := os.Remove(downloadPath); err != nil {
			release.err = fmt.Errorf("error deleting partially downloaded distro: %w", err)
			release.flush(internal, true)
		}
		return err // todo handle temporary/recoverable errors
	}

	if err = os.Rename(downloadPath, destination); err != nil {
		return fmt.Errorf("error renaming downloaded distro at '%s': %w", downloadPath, err)
	}

	return nil
}

func (release *release) extractDistro(internal *releaseInternal, cachePath string, extractionPath string) error {
	distro, err := os.Open(cachePath)
	if err != nil {
		return fmt.Errorf("error opening distro at '%s': %w", cachePath, err)
	}
	defer distro.Close()

	defer func() {
		// in case anything immediately returned without updating,
		// assuming that `reset` would automatically do so
		release.flush(internal, true)

		// Even if extraction failed, that implies a possibly corrupted distro, so still remove it
		if err = os.Remove(cachePath); err != nil {
			release.err = fmt.Errorf("error deleting distro at '%s': %w", cachePath, err)
			release.flush(internal, true)
		}
	}()

	format := archives.CompressedArchive{
		Extraction:  archives.Tar{},
		Compression: archives.Brotli{},
	}
	if err = format.Extract(release.ctx, distro, func(ctx context.Context, info archives.FileInfo) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !strings.HasPrefix(info.NameInArchive, "files/") {
			log.Println("Skipping extraction of " + info.NameInArchive)
			return nil
		}

		name := info.NameInArchive[len("files/"):]

		release.message = "Extracting " + name

		if info.IsDir() {
			if err = os.MkdirAll(filepath.Join(extractionPath, name), info.Mode().Perm()); err != nil {
				return fmt.Errorf("error creating extracted directory '%s': %w", name, err)
			}
			return nil
		}

		source, err := info.Open()
		if err != nil {
			return fmt.Errorf("error opening extracted file '%s': %w", info.NameInArchive, err)
		}
		defer source.Close()

		if err = os.MkdirAll(filepath.Join(extractionPath, filepath.Dir(name)), 0755); err != nil {
			return fmt.Errorf("error creating parent directories for '%s': %w", name, err)
		}

		destination, err := os.OpenFile(filepath.Join(extractionPath, name), os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return fmt.Errorf("error opening destination file '%s': %w", filepath.Join(extractionPath, name), err)
		}
		defer destination.Close()

		buffer := make([]byte, 32*1024)
		accumulated := 0
		finished := false
		for !finished {
			n, err := source.Read(buffer)
			if err != nil {
				if err != io.EOF {
					return fmt.Errorf("error reading extracted file '%s': %w", info.NameInArchive, err)
				}

				finished = true
			}
			accumulated += n
			release.progress = uint8(float64(accumulated) / float64(info.Size()) * 100)
			release.flush(internal, true)

			if _, err = destination.Write(buffer[:n]); err != nil {
				return fmt.Errorf("error writing extracted file '%s': %w", name, err)
			}
		}

		return nil
	}); err != nil {
		release.err = fmt.Errorf("error extracting tarball: %w", err)
		release.flush(internal, true)
		// TODO do this atomically by extracting into a tmpdir and renaming on success instead of overwriting preexisting install in case of failure
		if err := os.RemoveAll(extractionPath); err != nil {
			release.err = fmt.Errorf("error removing extracted tarball: %w", err)
		}
		return err
	}

	return nil
}

func (release *release) install() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	// even if installing Discord fails for whatever reason,
	// BetterDiscord should still be updated
	defer func() {
		go release.applyBd()
	}()

	installed := internal.InstalledVersion != ""

	manifest, err := release.getLatestManifest(internal)
	if err != nil {
		return
	}

	if installed && internal.LatestVersion != "" && internal.InstalledVersion == internal.LatestVersion {
		return
	}

	release.status = statusInstall
	release.flush(internal, true)

	cache, err := getCacheDislaunchDirectory()
	if err != nil {
		release.err = fmt.Errorf("error getting cache Dislaunch directory: %w", err)
		return
	}

	moduleCachePath := func(name string, manifest manifestPackageVersion) string {
		return filepath.Join(cache, fmt.Sprintf("%s-%s-%s-%s.distro", release.id, name, versionArrayToString(manifest.HostVersion), strconv.Itoa(*manifest.ModuleVersion)))
	}

	downloaded := []string{}

	release.message = "Downloading version " + versionArrayToString(manifest.Full.HostVersion)
	appCachePath := filepath.Join(cache, release.id+"-"+versionArrayToString(manifest.Full.HostVersion)+".distro")
	if err = release.downloadDistro(internal, manifest.Full, appCachePath); err != nil {
		release.err = fmt.Errorf("error downloading application: %w", err)
		return
	}
	downloaded = append(downloaded, appCachePath)

	for name, module := range manifest.Modules {
		release.message = "Downloading module " + name

		cachePath := moduleCachePath(name, module.Full)
		if err = release.downloadDistro(internal, module.Full, cachePath); err != nil {
			release.err = fmt.Errorf("error downloading module '%s': %w", name, err)
			return
		}
		downloaded = append(downloaded, cachePath)
	}

	if entries, err := os.ReadDir(cache); err == nil {
		for _, entry := range entries {
			path := filepath.Join(cache, entry.Name())

			if slices.Contains(downloaded, path) || !strings.HasPrefix(path, release.id) {
				continue
			}

			if err = os.Remove(path); err != nil {
				release.err = fmt.Errorf("error removing cached file '%s': %w", path, err)
				release.flush(internal, true)
			}
		}
	} else {
		release.err = fmt.Errorf("error getting entries of cache directory: %w", err)
		release.flush(internal, true)
	}

	installPath, err := release.getInstallPath(versionArrayToString(manifest.Full.HostVersion))
	if err != nil {
		return
	}

	if err = os.MkdirAll(installPath, 0755); err != nil {
		release.err = fmt.Errorf("error creating install path at '%s': %w", installPath, err)
		return
	}

	if installed {
		installRealpath, err := filepath.EvalSymlinks(installPath)
		if err != nil {
			release.err = fmt.Errorf("error getting realpath of install path '%s': %w", filepath.Join(installRealpath, release.pathName), err)
			return
		}
		processes, err := process.Processes()
		if err != nil {
			release.err = fmt.Errorf("error getting running processes: %w", err)
			return
		}
		for _, process := range processes {
			exe, err := process.Exe()
			if err != nil {
				release.err = fmt.Errorf("error getting executable of running process: %w", err)
				release.flush(internal, true)
				continue
			}

			exeRealpath, err := filepath.EvalSymlinks(exe)
			if err != nil {
				release.err = fmt.Errorf("error getting realpath of executable '%s': %w", exe, err)
				release.flush(internal, true)
				continue
			}

			if strings.HasPrefix(exeRealpath, installRealpath) {
				log.Println("Release '", release, "' is currently running - skipping install")
				return
			}
		}
	}

	if err = release.extractDistro(internal, appCachePath, installPath); err != nil {
		release.err = fmt.Errorf("error extracting application: %w", err)
		return
	}

	for name, module := range manifest.Modules {
		if err = release.extractDistro(internal, moduleCachePath(name, module.Full), filepath.Join(installPath, "modules", name+"-"+strconv.Itoa(*module.Full.ModuleVersion), name)); err != nil {
			release.err = fmt.Errorf("error extracting module '%s': %w", name, err)
			return
		}
	}

	internal.InstalledVersion = versionArrayToString(manifest.Full.HostVersion)

	icon := filepath.Join(installPath, "discord.png")
	icons := filepath.Join(getHomeXdgDirectory("XDG_DATA_HOME", filepath.Join(".local", "share")), "icons")
	if err = os.MkdirAll(icons, 0755); err == nil {
		release.message = "Copying icon"
		release.flush(internal, true)
		for _, path := range []string{filepath.Join(icons, "256x256"), filepath.Join(icons, "128x128@2")} {
			if err = os.Mkdir(path, 0755); err != nil && !errors.Is(err, os.ErrNotExist) {
				release.err = fmt.Errorf("error creating icon subdirectory '%s': %w", path, err)
				release.flush(internal, true)
				continue
			}

			destination := "discord.png"
			if release == canary {
				destination = "discord-canary.png"
			}

			if err = cp.Copy(icon, filepath.Join(path, destination)); err != nil {
				release.err = fmt.Errorf("error copying icon from '%s' to '%s': %w", icon, path, err)
				release.flush(internal, true)
			}
		}
	} else {
		release.err = fmt.Errorf("error creating user icon directory '%s': %w", icons, err)
		release.flush(internal, true)
	}

	desktopEntry := ""

	home, err := os.UserHomeDir()
	if err != nil {
		release.err = fmt.Errorf("error getting home directory: %w", err)
		return
	}

	oldExec := "Exec=" + filepath.Join("/", "usr", "share", release.desktopEntryFileName[:strings.IndexByte(release.desktopEntryFileName, '.')], release.pathName)
	newExec := "Exec=" + filepath.Join(home, ".local", "bin", "dislaunch") + " " + release.id
	dislaunchDesktopEntry := strings.ReplaceAll(desktopEntry, oldExec, newExec)

	dislaunchDesktopEntryFile, err := os.OpenFile(filepath.Join(getHomeXdgDirectory("XDG_DATA_HOME", filepath.Join(".local", "share")), "applications", release.desktopEntryFileName), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		release.err = fmt.Errorf("error opening .desktop file: %w", err)
		return
	}
	defer dislaunchDesktopEntryFile.Close()

	release.message = "Writing desktop entry"
	accumulated := 0
	for accumulated < len(dislaunchDesktopEntry) {
		n, err := dislaunchDesktopEntryFile.Write([]byte(dislaunchDesktopEntry)[accumulated:])
		if err != nil {
			release.err = fmt.Errorf("error writing to desktop file: %w", err)
			return
		}
		accumulated += n
		release.progress = uint8(float64(accumulated) / float64(len(dislaunchDesktopEntry)) * 100)
		release.flush(internal, true)
	}
}

func (release *release) uninstall() {
	internal, reset := release.takeOver()
	if internal == nil || reset == nil {
		return
	}
	defer reset()

	if internal.InstalledVersion == "" {
		return
	}

	installPath, err := release.getInstallPath(internal.InstalledVersion)
	if err != nil {
		return
	}

	release.status = statusUninstall
	release.message = "Deleting " + installPath
	release.progress = 101
	release.flush(internal, true)

	// Scary!
	// todo perhaps consider some safeguards to prevent deleting critical directories?
	if err := os.RemoveAll(installPath); err != nil {
		release.status = statusFatal
		release.err = fmt.Errorf("error uninstalling release '%s' from '%s': %w", release, installPath, err)
		release.flush(internal, true)
	}

	if err := os.Remove(filepath.Join(getHomeXdgDirectory("XDG_DATA_HOME", filepath.Join(".local", "share")), "applications", release.desktopEntryFileName)); err != nil {
		release.status = statusFatal
		release.err = fmt.Errorf("error deleting desktop entry for release '%s': %w", release, err)
		release.flush(internal, true)
	}

	internal.InstalledVersion = ""
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
			return err
		}
		internal.BdLatestRelease = bdRelease.ID
	case bdCanary:
		releases, _, err := client.Repositories.ListReleases(release.ctx, "BetterDiscord", "BetterDiscord", &github.ListOptions{Page: 1, PerPage: 1})
		if err != nil {
			release.err = fmt.Errorf("error getting BetterDiscord releases: %w", err)
			return err
		}
		internal.BdLatestRelease = releases[0].ID
	default:
		release.err = fmt.Errorf("invalid BetterDiscord release channel: %s", internal.BdChannel)
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

	if internal.InstalledVersion == "" {
		return
	}

	release.status = statusBdInjection
	release.flush(internal, true)

	// no need to `os.MkdirAll` here, I already do it later
	config, err := os.UserConfigDir()
	if err != nil {
		release.err = fmt.Errorf("error getting user config directory: %w", err)
		return
	}

	path := filepath.Join(config, strings.ToLower(release.pathName), internal.InstalledVersion, "modules", "discord_desktop_core")

	if internal.BdEnabled {
		if internal.BdLatestRelease == nil && release.checkForBdUpdates(internal) != nil {
			return
		}

		if internal.BdInstalledRelease == nil || *internal.BdInstalledRelease != *internal.BdLatestRelease {
			client := github.NewClient(nil)
			bdRelease, _, err := client.Repositories.GetRelease(release.ctx, "BetterDiscord", "BetterDiscord", *internal.BdLatestRelease)
			if err != nil {
				release.err = fmt.Errorf("error getting latest BetterDiscord release: %w", err)
				return
			}

			for _, asset := range bdRelease.Assets {
				if *asset.Name != "betterdiscord.asar" {
					continue
				}

				if err = os.MkdirAll(path, 0755); err != nil {
					release.err = fmt.Errorf("error creating '%s': %w", path, err)
					return
				}

				asarPath := filepath.Join(path, "betterdiscord.asar")

				asar, err := os.OpenFile(asarPath, os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					release.err = fmt.Errorf("error opening '%s': %w", asarPath, err)
					return
				}

				release.message = "Downloading BetterDiscord"
				release.flush(internal, true)

				if err = download(release.ctx, *asset.BrowserDownloadURL, asar, func(progress uint8) {
					release.progress = progress
					release.flush(internal, true)
				}); err != nil {
					release.err = fmt.Errorf("error downloading BetterDiscord: %w", err)
					return
				}

				internal.BdInstalledRelease = internal.BdLatestRelease

				break
			}
		}
	} else {
		if internal.BdInstalledRelease == nil {
			// no need to remove it, it shouldn't be installed or injected anyway
			return
		}

		release.message = "Removing BetterDiscord"

		if err = os.Remove(filepath.Join(path, "betterdiscord.asar")); err != nil && !errors.Is(err, os.ErrNotExist) {
			release.err = fmt.Errorf("error deleting BetterDiscord: %w", err)
			release.flush(internal, true)
		}

		internal.BdInstalledRelease = nil
		internal.BdLatestRelease = nil
	}
	release.flush(internal, true)

	indexJsPath := filepath.Join(path, "index.js")
	indexJs, err := os.OpenFile(indexJsPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		release.err = fmt.Errorf("error opening '%s': %w", indexJsPath, err)
		return
	}

	var content string
	if internal.BdEnabled {
		content = "require(\"./betterdiscord.asar\");\nmodule.exports = require(\"./core.asar\");"
		release.message = "Injecting BetterDiscord"
		release.flush(internal, true)
	} else {
		content = "module.exports = require('./core.asar');"
	}

	accumulated := 0
	for accumulated < len(content) {
		n, err := indexJs.WriteString(content)
		if err != nil {
			release.err = fmt.Errorf("error writing to '%s': %w", indexJsPath, err)
			return
		}
		accumulated += n
	}
}
