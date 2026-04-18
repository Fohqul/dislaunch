package dislaunch

import (
	"errors"
	"fmt"
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

func getHomeXdgDirectory(environment string, fallback string) string {
	if directory := os.Getenv(environment); directory != "" {
		return directory
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("error getting user home directory: %s", err) // if we couldn't get the user's home directory, then the situation is so fucked we may as well crash
	}
	return filepath.Join(home, fallback)
}

func getHomeXdgDislaunchDirectory(environment string, fallback string) string {
	directory := filepath.Join(getHomeXdgDirectory(environment, fallback), "io.github.Fohqul.Dislaunch")
	stat, err := os.Stat(directory)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Fatalf("error accessing XDG directory in user home '%s': %s\n", directory, err)
		}

		if err := os.MkdirAll(directory, 0700); err != nil {
			log.Fatalf("error creating XDG directory in user home '%s': %s\n", directory, err)
		}
	} else if !stat.IsDir() {
		log.Fatal("XDG path in user home is not a directory: " + directory)
	}
	return directory
}

func getCacheDislaunchDirectory() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("error getting user cache directory: %w", err)
	}

	path := filepath.Join(cache, "io.github.Fohqul.Dislaunch")

	if err := os.MkdirAll(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("error creating cache Dislaunch directory at '%s': %w", path, err)
	}

	return path, nil
}
