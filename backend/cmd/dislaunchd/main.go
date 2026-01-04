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

func main() {
	lockfilePath := filepath.Join(dislaunch.GetRuntimeDirectory(), "dislaunch.sock")
	lockfile := flock.New(lockfilePath)
	if locked, err := lockfile.TryLock(); err != nil {
		log.Fatalf("error locking at '%s': %w\n", lockfilePath, err)
	} else if !locked {
		log.Fatalf("error locking at '%s'", lockfilePath)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	close, err := dislaunch.StartListener()
	if err != nil {
		if err = lockfile.Unlock(); err != nil {
			fmt.Fprintf(os.Stderr, "error unlocking at '%s': %w\n", lockfilePath, err)
		}
		log.Fatalf("error starting listener: %w\n", err)
	}

	<-signals
	close()
	if err = lockfile.Unlock(); err != nil {
		log.Fatalf("error unlocking at '%s': %w\n", lockfilePath, err)
	}
}
