package dislaunch

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

func GetRuntimeDirectory() string {
	runtimeDirectory := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDirectory == "" {
		runtimeDirectory = filepath.Join(os.TempDir(), "io.github.Fohqul.Dislaunch-"+string(os.Getuid()))
	}
	if err := os.MkdirAll(runtimeDirectory, 0700); err != nil {
		log.Fatalf("error creating runtime directory at '%s': %w\n", runtimeDirectory, err)
	}

	return runtimeDirectory
}

func GetHomeXDGDirectory(environment string, fallback string) string {
	directory := os.Getenv(environment)
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err) // if we couldn't get the user's home directory, then the situation is so fucked we may as well crash
		}
		directory = filepath.Join(home, fallback)
	}

	directory = filepath.Join(directory, "io.github.Fohqul.Dislaunch")
	stat, err := os.Stat(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(directory, 0700); err != nil {
				log.Fatal(err)
			}
		} else {
			log.Fatal(err)
		}
	} else if !stat.IsDir() {
		log.Fatal("XDG directory path is a file")
	}
	return directory
}

func GetDataHome() string {
	return GetHomeXDGDirectory("XDG_DATA_HOME", filepath.Join(".local", "share"))
}
