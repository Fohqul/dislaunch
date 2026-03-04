package dislaunch

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gen2brain/beeep"
)

func runInterval(configuration Configuration, release *Release) {
	release.CheckForUpdates()
	state := release.GetState()
	if state.Version == state.Internal.LatestVersion {
		return
	}

	if configuration.NotifyOnUpdateAvailable {
		message := "There's an update available for Discord."
		switch release {
		case &PTB:
			message = "There's an update available for Discord PTB."
		case &Canary:
			message = "There's an update available for Discord Canary."
		}
		if err := beeep.Notify("Update available", message, "software-update-available"); err != nil {
			fmt.Fprintf(os.Stderr, "error sending notification: %s\n", err)
		}
	}

	if configuration.AutomaticallyInstallUpdates {
		release.Install()
	}
}

func StartIntervals() {
	for range time.Tick(10 * time.Minute) {
		configuration := GetConfiguration()
		if !configuration.AutomaticallyCheckForUpdates {
			continue
		}

		var wg sync.WaitGroup
		for _, release := range []*Release{&Stable, &PTB, &Canary} {
			wg.Go(func() {
				runInterval(configuration, release)
			})
		}
		wg.Wait() // if any of the goroutines take longer to finish than the interval, stop them accumulating
	}
}
