package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	dislaunch "github.com/Fohqul/dislaunch/internal"
	"github.com/gofrs/flock"
)

func unlock(lockfile *flock.Flock) {
	if err := lockfile.Unlock(); err != nil {
		fmt.Fprintf(os.Stderr, "error unlocking at '%s': %w\n", lockfile.Path(), err)
	}
}

func main() {
	lockfilePath := filepath.Join(dislaunch.GetRuntimeDirectory(), "dislaunch.sock")
	lockfile := flock.New(lockfilePath)
	if locked, err := lockfile.TryLock(); err != nil {
		log.Fatalf("error locking at '%s': %w\nIs another instance of Dislaunch already running?\n", lockfilePath, err)
	} else if !locked {
		log.Fatalf("error locking at '%s'\nIs another instance of Dislaunch already running?\n", lockfilePath)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	close, err := dislaunch.StartListener()
	if err != nil {
		unlock(lockfile)
		log.Fatalf("error starting listener: %w\n", err)
	}

	<-signals
	close()
	unlock(lockfile)
}
