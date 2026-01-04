package dislaunch

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type Configuration struct {
	AutomaticallyCheckForUpdates bool   `json:"automatically_check_for_updates"`
	NotifyOnUpdateAvailable      bool   `json:"notify_on_update_available"`
	AutomaticallyInstallUpdates  bool   `json:"automatically_install_updates"`
	DefaultInstallPath           string `json:"default_install_path"`
	Error                        error  `json:"error"`
}

func openConfigurationFile() *os.File {
	configurationDirectory, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("error getting configuration directory: %w\n", err)
	}

	configurationFile, err := os.Open(filepath.Join(configurationDirectory, "io.github.Fohqul.Dislaunch.json"))
	if err != nil {
		log.Fatalf("error opening configuration file: %w\n", err)
	}
	return configurationFile
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
	configurationFile := openConfigurationFile()
	defer configurationFile.Close()

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

func SetConfiguration(configuration Configuration) error {
	stat, err := os.Stat(configuration.DefaultInstallPath)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("cannot install to non-directory: %s", configuration.DefaultInstallPath)
	}

	if err = assertWritePermissions(configuration.DefaultInstallPath); err != nil {
		return err
	}

	configurationFile := openConfigurationFile()
	defer configurationFile.Close()

	if err = json.NewEncoder(configurationFile).Encode(configuration); err != nil {
		return err // todo should this be fatal?
	}

	return nil
}
