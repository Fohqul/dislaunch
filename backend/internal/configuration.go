package dislaunch

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"
)

type Configuration struct {
	AutomaticallyCheckForUpdates bool   `json:"automatically_check_for_updates"`
	NotifyOnUpdateAvailable      bool   `json:"notify_on_update_available"`
	AutomaticallyInstallUpdates  bool   `json:"automatically_install_updates"`
	DefaultInstallPath           string `json:"default_install_path"`
}

func openConfigurationFile() (*os.File, func()) {
	configurationDirectory, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("error getting configuration directory: %w\n", err)
	}

	path := filepath.Join(configurationDirectory, "io.github.Fohqul.Dislaunch.json")
	lock := flock.New(path)

	configurationFile, err := os.Open(path)
	if err != nil {
		log.Fatalf("error opening configuration file: %w\n", err)
	}
	return configurationFile, func() {
		configurationFile.Close()
		lock.Unlock()
	}
}

func assertWritePermissions(path string) error {
	file, err := os.CreateTemp(path, ".dislaunch-write-confirmation-*")
	if err != nil {
		return err
	}
	file.Close()
	if err = os.Remove(file.Name()); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting temporary file '%s': %w\n", file.Name(), err)
	}
	return nil
}

func GetConfiguration() Configuration {
	configurationFile, close := openConfigurationFile()
	defer close()

	var configuration Configuration
	if err := json.NewDecoder(configurationFile).Decode(&configuration); err != nil {
		fmt.Fprintf(os.Stderr, "error decoding configuration: %w\n", err)
	}

	if err := assertWritePermissions(configuration.DefaultInstallPath); err != nil {
		fmt.Fprintf(os.Stderr, "error writing to configured install location '%s': %w\n", configuration.DefaultInstallPath, err)
		configuration.DefaultInstallPath = GetHomeXDGDirectory("XDG_DATA_HOME", filepath.Join(".local", "share"))
	}
	return configuration
}

func setConfiguration(configuration Configuration) error {
	configurationFile, close := openConfigurationFile()
	defer close()

	if err := json.NewEncoder(configurationFile).Encode(configuration); err != nil {
		return err // todo should this be fatal?
	}

	return nil
}

// Mutex prevents multiple writes happening at once.
// Whilst there is already a flock on the configuration
// file, the set functions must first get the current
// configuration, modify the relevant setting and then
// write the configuration back to the file. During the
// modify, another set to a different option may occur,
// which would leave the stored configuration stale and
// would reset the different option upon being written.
// Therefore, a mutex is used to prevent this.
var mu sync.Mutex

func SetAutomaticallyCheckForUpdates(setting bool) {
	mu.Lock()
	defer mu.Unlock()

	configuration := GetConfiguration()
	configuration.AutomaticallyCheckForUpdates = setting
	setConfiguration(configuration)
}

func SetNotifyOnUpdateAvailable(setting bool) {
	mu.Lock()
	defer mu.Unlock()

	configuration := GetConfiguration()
	configuration.NotifyOnUpdateAvailable = setting
	setConfiguration(configuration)
}

func SetAutomaticallyInstallUpdates(setting bool) {
	mu.Lock()
	defer mu.Unlock()

	configuration := GetConfiguration()
	configuration.AutomaticallyInstallUpdates = setting
	setConfiguration(configuration)
}

func SetDefaultInstallPath(path string) error {
	mu.Lock()
	defer mu.Unlock()

	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("cannot install to non-directory: %s", path)
	}

	if err = assertWritePermissions(path); err != nil {
		return err
	}

	configuration := GetConfiguration()
	configuration.DefaultInstallPath = path
	setConfiguration(configuration)
	return nil
}
