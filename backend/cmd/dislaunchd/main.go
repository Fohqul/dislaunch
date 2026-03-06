package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	dislaunch "github.com/Fohqul/dislaunch/internal"
	"github.com/gofrs/flock"
)

const STARTED = "DAEMON STARTED"

func usage() {
	fmt.Printf("Usage: %s [command]\n", os.Args[0])
	fmt.Println("\tpath\tGet path of running socket and start one if none is running")
	fmt.Println("\tstart\tStart the daemon")
	fmt.Printf("\t\t-p\tPrint \"%s\" when the daemon successfully starts\n", STARTED)
}

func unlock(lockfile *flock.Flock) {
	if err := lockfile.Unlock(); err != nil {
		fmt.Fprintf(os.Stderr, "error unlocking at '%s': %s\n", lockfile.Path(), err)
	}
}

func main() {
	if len(os.Args) == 1 {
		usage()
		return
	}

	lockfilePath := filepath.Join(dislaunch.GetRuntimeDirectory(), "dislaunch.sock")
	lockfile := flock.New(lockfilePath)

	switch os.Args[1] {
	case "path":
		locked, err := lockfile.TryLock()
		if err != nil && !errors.Is(err, syscall.ENXIO) {
			log.Fatalf("error trying to lock at '%s': %s\n", lockfilePath, err)
		}
		if !locked {
			fmt.Print(lockfilePath)
			return
		}

		cmd := exec.Command(os.Args[0], "start", "-p")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Fatalf("error getting standard output pipe to daemon command: %s\n", err)
		}

		var wg sync.WaitGroup
		started := make(chan byte)
		wg.Go(func() {
			reader := bufio.NewReader(stdout)
			for {
				if line, _, err := reader.ReadLine(); err != nil {
					log.Fatalf("error reading from daemon standard output: %s\n", err)
				} else if string(line) == STARTED {
					started <- 0
					return
				}
			}
		})

		timer := time.NewTimer(5 * time.Second)
		wg.Go(func() {
			select {
			case <-timer.C:
				log.Fatal("daemon timed out")
			case <-started:
				fmt.Print(lockfilePath)
				os.Exit(0)
			}
		})

		unlock(lockfile)
		if err = cmd.Start(); err != nil {
			log.Fatalf("error starting daemon process: %s\n", err)
		}
		wg.Wait()
	case "start":
		if locked, err := lockfile.TryLock(); err != nil {
			log.Fatalf("error locking at '%s': %s\nIs another instance of Dislaunch already running?\n", lockfilePath, err)
		} else if !locked {
			log.Fatalf("error locking at '%s'\nIs another instance of Dislaunch already running?\n", lockfilePath)
		}
		defer unlock(lockfile)

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		close, err := dislaunch.StartListener()
		if err != nil {
			log.Fatalf("error starting listener: %s\n", err)
		}
		defer close()

		if len(os.Args) > 2 && os.Args[2] == "-p" {
			fmt.Printf("\n%s\n", STARTED) // surround with newlines so it's guaranteed to be picked up as a single line
		}

		<-signals
	default:
		fmt.Fprintf(os.Stderr, "invalid arguments: %s\n", strings.Join(os.Args[1:], " "))
		usage()
		os.Exit(1)
	}
}
