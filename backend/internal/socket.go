package dislaunch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var listener net.Listener

type connectionEntry struct {
	wg    sync.WaitGroup
	write chan []byte
	close chan byte
}

type connectionsContainer struct {
	mu          sync.Mutex
	connections map[net.Conn]*connectionEntry
}

var container connectionsContainer

func connectionOpen(conn net.Conn) bool {
	container.mu.Lock()
	defer container.mu.Unlock()

	if _, exists := container.connections[conn]; listener == nil || !exists {
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
	switch setting {
	case "0":
		set(false)
	case "1":
		set(true)
	default:
		fmt.Fprintf(os.Stderr, "invalid boolean setting: %s\n", setting)
	}
}

func startReader(conn net.Conn, entry *connectionEntry) {
	reader := bufio.NewReader(conn)

	for connectionOpen(conn) {
		data, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
				break
			}

			fmt.Fprintf(os.Stderr, "error reading buffered I/O: %s\n", err)
			continue
		}
		log.Println("Connection received:", data)
		command := strings.Fields(data)

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

	entry.close <- 0
}

func startWriter(conn net.Conn, entry *connectionEntry) {
	for message := range entry.write {
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			fmt.Fprintf(os.Stderr, "error setting write deadline: %s\n", err)
		}

		if _, err := conn.Write(message); err != nil {
			if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
				break
			} else {
				fmt.Fprintf(os.Stderr, "error writing to connection: %s\nMessage: %s", err, message)
			}
		}
	}

	entry.wg.Done()
	entry.close <- 0
}

func handleConnection(conn net.Conn) {
	container.mu.Lock()
	if _, exists := container.connections[conn]; exists {
		fmt.Fprintf(os.Stderr, "connection %p already exists?\n", conn)
		container.mu.Unlock()
		return
	}
	entry := &connectionEntry{write: make(chan []byte), close: make(chan byte)}
	container.connections[conn] = entry
	container.mu.Unlock()
	log.Println("Accepted connection", conn)

	go startReader(conn, entry)
	entry.wg.Go(func() {
		startWriter(conn, entry)
	})

	<-entry.close
	container.mu.Lock()
	delete(container.connections, conn)
	container.mu.Unlock()
	log.Println("Closing connection", conn)
	close(entry.write)
	entry.wg.Wait()
	if err := conn.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "error closing connection: %s\n", err)
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

	var err error // if `:=` is used on the next line, it shadows the global `listener`
	listener, err = net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("error creating socket at '%s': %w\n", socket, err)
	}
	log.Println("Listener started at " + socket)

	container.connections = make(map[net.Conn]*connectionEntry)

	go func() {
		for {
			if listener == nil {
				return
			}
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

		container.mu.Lock()
		defer container.mu.Unlock()
		for _, entry := range container.connections {
			entry.close <- 0
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
	log.Println("Sending global state:", string(message))

	container.mu.Lock()
	defer container.mu.Unlock()
	for _, entry := range container.connections {
		entry.write <- message
	}
}
