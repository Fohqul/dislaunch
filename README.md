# dislaunch

Your last lucky day is here.

## What?

Dislaunch acts as a launcher and an automatic updater for Discord on Linux. Because Discord doesn't automatically download and install the latest versions on Linux as it does on other operating systems, you have to either manually do it yourself after each update or rely on [less than ideal solutions](#why).

Dislaunch works by providing its own `.desktop` entry which you use to open Discord and pin it to your dock/panel/taskbar/task manager on your desktop environment. In doing so, it "intercepts" the user's attempt to launch Discord, during which it checks for updates and automatically installs them before actually executing Discord. This makes for an experience that's just as seamless as on other operating systems.

## Why?

More specifically, why use Dislaunch over other existing automatic-update solutions, like Flatpaks and the AUR? Briefly:

- Flatpak, thanks to sandboxing, has spotty support for functionality like AppIndicators/notification badges and screensharing
- AUR and Pacstall packages rely on a maintainer manually keeping them up-to-date with the latest versions
	- Since these packages aren't always up-to-date, you must intentionally inhibit updates to suppress the "Your lucky day is here!" dialogue while you wait for the maintainer to publish an update, meaning you aren't always up-to-date
	- This may not be so bad for users of the stable release channel, but PTB and Canary users are hit hard by this
- Neither of the above support automatically injecting BetterDiscord on each update (although this feature is still to-do in Dislaunch)

## Building from source

### Prerequisites

- Go >=1.25
- Meson >=1.10
- A C compiler (GCC >=15.2 or Clang >=22.1)
- Vala >=0.56
- GLib >=2.86
- libgee >=0.20
- GIO
- JSON-GLib >=1.10
- GTK >=4.20
- Libadwaita >=1.8

Note that these requirements aren't based on a strict analysis of what features are actually used, I'm just going off whatever my installed versions of these dependencies are. You may or may not have success with older versions of these (although you will definitely need Go 1.25 or above.)

### Backend

```sh
cd backend
go build ./cmd/dislaunchd
```

This produces a binary `dislaunchd`. It doesn't matter where you put this, so long as it's accessible from `PATH`.

### Frontend

```sh
meson setup builddir
meson compile -C builddir
```

This produces 2 executables, both under `builddir/frontend`: `dislaunch` and `dislaunchctl`. For technical reasons, the former must be placed in `~/.local/bin` (the backend makes this assumption when generating the `.desktop` entries, this should probably be changed.) The latter is an optional extra, and you can do whatever you want with it (including not using it at all.)

## AI declaration

Throughout this project (including the old [`discord-linux-updater`](https://github.com/Fohqul/discord-linux-updater)), I've used ChatGPT for debugging and as a learning aid or for suggestions, ideas and reviews (e.g. I only know that IPC and UNIX sockets are a thing thanks to it). Sometimes I’ve also used ChatGPT when I was really stuck on how to solve a specific problem. All that said, I write all the code and consider it my work.