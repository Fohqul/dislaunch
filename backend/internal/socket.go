package dislaunch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var listener net.Listener

type connectionsContainer struct {
	mu          sync.Mutex
	connections map[net.Conn]struct{}
}

var container connectionsContainer

func handleConnection(conn net.Conn) {
	if _, exists := container.connections[conn]; exists {
		fmt.Fprintf(os.Stderr, "connection %p already exists?\n", conn)
		return
	}
	container.connections[conn] = struct{}{} // TODO cleanup and close dead connections
	reader := bufio.NewReader(conn)

	for {
		data, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading buffered I/O: %w\n", err)
			continue
		}
		action := strings.Split(data, " ")

		var release Release
		switch action[0] {
		case "state":
			BroadcastGlobalState()
			break
		case "stable":
			release = Stable
		case "ptb":
			release = PTB
		case "canary":
			release = Canary
			switch action[1] {
			case "bd":
				switch action[2] {
				case "stable":
					go release.InjectBetterDiscord(BDStable)
				case "canary":
					go release.InjectBetterDiscord(BDCanary)
				default:
					fmt.Fprintf(os.Stderr, "unknown BetterDiscord channel: %s\n", action[2])
				}
				break
			case "check_for_updates":
				go release.CheckForUpdates()
				break
			case "install":
				go release.Install()
				break
			case "move":
				go release.Move(action[2])
			case "uninstall":
				go release.Uninstall()
				break
			default:
				fmt.Fprintf(os.Stderr, "unknown argument: %s\n", action[1])
			}
			break
		case "config":
			// TODO
			break
		default:
			fmt.Fprintf(os.Stderr, "unknown action: %s\n", action[0])
		}
	}
}

func StartListener() (func(), error) {
	if listener != nil {
		return nil, fmt.Errorf("listener already started")
	}

	socket := filepath.Join(GetRuntimeDirectory(), "dislaunch.sock")
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("error removing existing socket at '%s': %w\n", socket, err)
	}

	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("error creating socket at '%s': %w\n", socket, err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error accepting connection: %w\n", err)
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
			fmt.Fprintf(os.Stderr, "error closing listener: %w\n", err)
		}

		for conn, _ := range container.connections {
			if err := conn.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "error closing connection: %w\n", err)
			}
		}
	}, nil
}

type ReleaseState struct {
	Internal ReleaseInternal    `json:"internal"`
	Version  string             `json:"version"`
	Process  ReleaseProcessView `json:"process"`
}

func getReleaseState(release Release) *ReleaseState {
	if !release.IsInstalled() {
		return nil
	}

	var state ReleaseState

	if internal, err := release.GetInternal(); err == nil {
		state.Internal = internal
	}

	if version, err := release.GetVersion(); err == nil {
		state.Version = version
	}

	state.Process = release.GetProcess().View()
	return &state
}

type GlobalState struct {
	Stable        *ReleaseState `json:"stable"`
	PTB           *ReleaseState `json:"ptb"`
	Canary        *ReleaseState `json:"canary"`
	Configuration Configuration `json:"config"`
}

func BroadcastGlobalState() {
	container.mu.Lock()
	defer container.mu.Unlock()

	buffer, err := json.Marshal(GlobalState{
		Stable:        getReleaseState(Stable),
		PTB:           getReleaseState(PTB),
		Canary:        getReleaseState(Canary),
		Configuration: GetConfiguration(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling global state to JSON: %w\n", err)
		return
	}
	message := append(buffer, '\n')

	for conn, _ := range container.connections {
		if _, err := conn.Write(message); err != nil {
			fmt.Fprintf(os.Stderr, "error writing global state to connection: %w\n", err)
			conn.Close()
		}
	}
}
