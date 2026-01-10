package dislaunch

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
)

func GetRuntimeDirectory() string {
	runtimeDirectory := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDirectory == "" {
		runtimeDirectory = filepath.Join(os.TempDir(), "io.github.Fohqul.Dislaunch-"+strconv.Itoa(os.Getuid()))
	}
	if err := os.MkdirAll(runtimeDirectory, 0700); err != nil {
		log.Fatalf("error creating runtime directory at '%s': %s\n", runtimeDirectory, err)
	}

	return runtimeDirectory
}

func GetHomeXDGDirectory(environment string, fallback string) string {
	directory := os.Getenv(environment)
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("error getting user home directory: %s", err) // if we couldn't get the user's home directory, then the situation is so fucked we may as well crash
		}
		directory = filepath.Join(home, fallback)
	}

	directory = filepath.Join(directory, "io.github.Fohqul.Dislaunch")
	stat, err := os.Stat(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(directory, 0700); err != nil {
				log.Fatalf("error creating XDG directory in user home '%s': %s\n", directory, err)
			}
		} else {
			log.Fatalf("error accessing XDG directory in user home '%s': %s\n", directory, err)
		}
	} else if !stat.IsDir() {
		log.Fatal("XDG path in user home is not a directory: " + directory)
	}
	return directory
}

func GetDataHome() string {
	return GetHomeXDGDirectory("XDG_DATA_HOME", filepath.Join(".local", "share"))
}
