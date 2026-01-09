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

func releaseCommand(release *Release, command []string) {
	switch command[1] {
	case "bd":
		switch command[2] {
		case "stable":
			go release.InjectBetterDiscord(BDStable)
			break
		case "canary":
			go release.InjectBetterDiscord(BDCanary)
			break
		default:
			fmt.Fprintf(os.Stderr, "unknown BetterDiscord channel: %s\n", command[2])
		}
		break
	case "check_for_updates":
		go release.CheckForUpdates()
		break
	case "install":
		go release.Install()
		break
	case "move":
		go release.Move(command[2])
	case "uninstall":
		go release.Uninstall()
		break
	default:
		fmt.Fprintf(os.Stderr, "unknown argument: %s\n", command[1])
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

	for {
		container.mu.Lock()
		if _, exists := container.connections[conn]; !exists {
			if err := conn.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "error closing connection: %w\n", err)
			}
			return
		}
		container.mu.Unlock()

		data, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
				container.mu.Lock()
				defer container.mu.Unlock()
				delete(container.connections, conn)
			} else {
				fmt.Fprintf(os.Stderr, "error reading buffered I/O: %w\n", err)
			}
			continue
		}
		command := strings.Split(data, " ")

		switch command[0] {
		case "state":
			go BroadcastGlobalState()
			break
		case "stable":
			go releaseCommand(&Stable, command)
			break
		case "ptb":
			go releaseCommand(&PTB, command)
			break
		case "canary":
			go releaseCommand(&Canary, command)
			break
		case "config":
			// TODO
			break
		default:
			fmt.Fprintf(os.Stderr, "unknown action: %s\n", command[0])
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

	container.connections = make(map[net.Conn]*sync.Mutex)

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
		container.mu.Lock()
		defer container.mu.Unlock()

		if listener == nil {
			fmt.Fprintln(os.Stderr, "error closing listener: no listener is running")
			return
		}

		if err := listener.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "error closing listener: %w\n", err)
		}

		for conn, _ := range container.connections {
			delete(container.connections, conn)
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
	container.mu.Lock()
	defer container.mu.Unlock()

	buffer, err := json.Marshal(GlobalState{
		Stable:        Stable.GetState(),
		PTB:           PTB.GetState(),
		Canary:        Canary.GetState(),
		Configuration: GetConfiguration(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling global state to JSON: %w\n", err)
		return
	}
	message := append(buffer, '\n')

	for conn, mu := range container.connections {
		go func() {
			mu.Lock()
			defer mu.Unlock()

			if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				fmt.Fprintf(os.Stderr, "error setting write deadline: %w\n", err)
			}

			if _, err := conn.Write(message); err != nil {
				if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
					container.mu.Lock()
					defer container.mu.Unlock()
					delete(container.connections, conn)
				} else {
					fmt.Fprintf(os.Stderr, "error writing global state to connection: %w\n", err)
				}
			}
		}()
	}
}
