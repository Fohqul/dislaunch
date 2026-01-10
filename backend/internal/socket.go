package dislaunch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var listener net.Listener

// `mu` represents a lock on reading from and writing to the
// `connections` map itself, whereas `connections[conn]`'s mutex
// represents a lock on writing to that connection
// todo there is almost certainly a better way of doing this
type connectionsContainer struct {
	mu          sync.Mutex
	connections map[net.Conn]*sync.Mutex
}

var container connectionsContainer

func closeConnection(conn net.Conn) {
	container.mu.Lock()
	delete(container.connections, conn)
	container.mu.Unlock()

	if err := conn.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "error closing connection: %s\n", err)
	}
}

func connectionOpen(conn net.Conn) bool {
	container.mu.Lock()
	defer container.mu.Unlock()

	if _, exists := container.connections[conn]; listener == nil || !exists {
		closeConnection(conn)
		return false
	}

	return true
}

func releaseCommand(release *Release, command []string) {
	switch command[1] {
	case "bd":
		switch command[2] {
		case "stable":
			go release.InjectBetterDiscord(BDStable)
		case "canary":
			go release.InjectBetterDiscord(BDCanary)
		default:
			fmt.Fprintf(os.Stderr, "unknown BetterDiscord channel: %s\n", command[2])
		}
	case "check_for_updates":
		go release.CheckForUpdates()
	case "install":
		go release.Install()
	case "move":
		go release.Move(command[2])
	case "uninstall":
		go release.Uninstall()
	default:
		fmt.Fprintf(os.Stderr, "unknown argument: %s\n", command[1])
	}
}

func setBoolean(set func(bool), setting string) {
	if setting == "0" {
		set(false)
	} else if setting == "1" {
		set(true)
	} else {
		fmt.Fprintf(os.Stderr, "invalid boolean setting: %s\n", setting)
	}
}

func handleConnection(conn net.Conn) {
	container.mu.Lock()
	if _, exists := container.connections[conn]; exists {
		fmt.Fprintf(os.Stderr, "connection %p already exists?\n", conn)
		container.mu.Unlock()
		return
	}
	container.connections[conn] = &sync.Mutex{}
	container.mu.Unlock()

	reader := bufio.NewReader(conn)

	for connectionOpen(conn) {
		data, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
				closeConnection(conn)
				return
			}

			fmt.Fprintf(os.Stderr, "error reading buffered I/O: %s\n", err)
			continue
		}
		command := strings.Split(data, " ")

		switch command[0] {
		case "state":
			go BroadcastGlobalState()
		case "stable":
			go releaseCommand(&Stable, command)
		case "ptb":
			go releaseCommand(&PTB, command)
		case "canary":
			go releaseCommand(&Canary, command)
		case "config":
			// i should be taken out back for nesting switch statements like this. there is certainly a better way of handling commands/subcommands/arguments
			switch command[1] {
			case "automatically_check_for_updates":
				setBoolean(SetAutomaticallyCheckForUpdates, command[2])
			case "notify_on_update_available":
				setBoolean(SetNotifyOnUpdateAvailable, command[2])
			case "automatically_install_updates":
				setBoolean(SetAutomaticallyInstallUpdates, command[2])
			case "default_install_path":
				if err = SetDefaultInstallPath(command[2]); err != nil {
					fmt.Fprintf(os.Stderr, "error setting default installation path: %s\n", err)
				}
			default:
				fmt.Fprintf(os.Stderr, "unknown configuration option: %s\n", command[1])
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown action: %s\n", command[0])
		}
	}
}

func StartListener() (func(), error) {
	if listener != nil {
		return nil, errors.New("listener already started")
	}

	socket := filepath.Join(GetRuntimeDirectory(), "dislaunch.sock")
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("error removing existing socket at '%s': %w\n", socket, err)
	}

	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("error creating socket at '%s': %w\n", socket, err)
	}

	container.connections = make(map[net.Conn]*sync.Mutex)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error accepting connection: %s\n", err)
				continue
			}
			go handleConnection(conn)
		}
	}()

	return func() {
		if listener == nil {
			fmt.Fprintln(os.Stderr, "error closing listener: no listener is running")
			return
		}

		if err := listener.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "error closing listener: %s\n", err)
		}
		listener = nil

		for conn, mu := range container.connections {
			go func() {
				mu.Lock()
				defer mu.Unlock()
				closeConnection(conn)
			}()
		}
	}, nil
}

type GlobalState struct {
	Stable        *ReleaseState `json:"stable"`
	PTB           *ReleaseState `json:"ptb"`
	Canary        *ReleaseState `json:"canary"`
	Configuration Configuration `json:"config"`
}

func BroadcastGlobalState() {
	if listener == nil {
		return
	}

	container.mu.Lock()
	defer container.mu.Unlock()

	buffer, err := json.Marshal(GlobalState{
		Stable:        Stable.GetState(),
		PTB:           PTB.GetState(),
		Canary:        Canary.GetState(),
		Configuration: GetConfiguration(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling global state to JSON: %s\n", err)
		return
	}
	message := append(buffer, '\n')

	for conn, mu := range container.connections {
		go func() {
			mu.Lock()
			defer mu.Unlock()

			if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				fmt.Fprintf(os.Stderr, "error setting write deadline: %s\n", err)
			}

			if _, err := conn.Write(message); err != nil {
				if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
					closeConnection(conn)
				} else {
					fmt.Fprintf(os.Stderr, "error writing global state to connection: %s\n", err)
				}
			}
		}()
	}
}
