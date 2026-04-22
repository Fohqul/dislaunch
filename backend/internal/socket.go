package dislaunch

import (
	"bufio"
	"context"
	"encoding/json/v2"
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
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	write  chan []byte
}

var container struct {
	mu          sync.Mutex
	connections map[net.Conn]*connectionEntry
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

func releaseCommand(release *release, data string, command []string) {
	switch command[1] {
	case "bd_apply":
		go release.applyBd()
	case "bd_enabled":
		if len(command) < 3 {
			fmt.Fprintln(os.Stderr, "setting required for BetterDiscord enabled")
			return
		}
		setBoolean(func(enabled bool) {
			go release.setBdEnabled(enabled)
		}, command[2])
	case "bd_channel":
		if len(command) < 3 {
			fmt.Fprintln(os.Stderr, "setting required for BetterDiscord channel")
			return
		}
		switch command[2] {
		case "stable":
			go release.setBdChannel(bdStable)
		case "canary":
			go release.setBdChannel(bdCanary)
		default:
			fmt.Fprintf(os.Stderr, "unknown BetterDiscord channel: %s\n", command[2])
		}
	case "cancel":
		release.cancel.Load().(context.CancelFunc)()
	case "check_for_updates":
		go release.checkForUpdates()
	case "command_line_arguments":
		// Use a slice directly from `data` so the raw arguments are kept as-is and not lost from `string.Fields`
		go release.setCommandLineArguments(data[len(release.String()+" command_line_arguments ") : len(data)-1])
	case "install":
		go release.install()
	case "move":
		if len(command) < 3 {
			fmt.Fprintln(os.Stderr, "path required to move release")
			return
		}
		go release.move(command[2])
	case "uninstall":
		go release.uninstall()
	default:
		fmt.Fprintf(os.Stderr, "unknown argument: %s\n", command[1])
	}
}

func startReader(conn net.Conn, entry *connectionEntry) {
	reader := bufio.NewReader(conn)

	for {
		select {
		case <-entry.ctx.Done():
			return
		default:
			data, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
					entry.cancel()
					return
				}

				fmt.Fprintf(os.Stderr, "error reading buffered I/O: %s\n", err)
				continue
			}

			log.Println("Connection received:", data)
			command := strings.Fields(data)
			if len(command) == 0 {
				continue
			}

			switch command[0] {
			case "state":
				go broadcastBackendState()
			case "stable":
				go releaseCommand(getStable(), data, command)
			case "ptb":
				go releaseCommand(getPtb(), data, command)
			case "canary":
				go releaseCommand(getCanary(), data, command)
			case "config":
				var argument string
				if len(command) > 2 {
					argument = command[2]
				}
				// i should be taken out back for nesting switch statements like this. there is certainly a better way of handling commands/subcommands/arguments
				switch command[1] {
				case "automatically_check_for_updates":
					setBoolean(setAutomaticallyCheckForUpdates, argument)
				case "notify_on_update_available":
					setBoolean(setNotifyOnUpdateAvailable, argument)
				case "automatically_install_updates":
					setBoolean(setAutomaticallyInstallUpdates, argument)
				case "default_install_path":
					if err = setDefaultInstallPath(argument); err != nil {
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
}

func startWriter(conn net.Conn, entry *connectionEntry) {
	for message := range entry.write {
		select {
		case <-entry.ctx.Done():
			log.Printf("Connection %p already closed - dropping message: %s\n", conn, message)
			continue
		default:
			if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				fmt.Fprintf(os.Stderr, "error setting write deadline: %s\n", err)
			}

			if _, err := conn.Write(message); err != nil {
				if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
					entry.cancel()
					return
				}

				fmt.Fprintf(os.Stderr, "error writing to connection: %s\nMessage: %s", err, message)
			}
		}
	}
}

func handleConnection(conn net.Conn) {
	container.mu.Lock()
	if _, exists := container.connections[conn]; exists {
		fmt.Fprintf(os.Stderr, "connection %p already exists?\n", conn)
		container.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	entry := &connectionEntry{
		ctx:    ctx,
		cancel: cancel,
		write:  make(chan []byte),
	}
	container.connections[conn] = entry
	container.mu.Unlock()
	log.Println("Accepted connection", conn)

	go startReader(conn, entry)
	entry.wg.Go(func() {
		startWriter(conn, entry)
	})

	<-entry.ctx.Done()
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
		for listener != nil {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error accepting connection: %s\n", err)
				continue
			}
			go handleConnection(conn)
		}
	}()

	go startIntervals()

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
			entry.cancel()
		}
	}, nil
}

type backendState struct {
	Stable        *releaseState `json:"stable"`
	Ptb           *releaseState `json:"ptb"`
	Canary        *releaseState `json:"canary"`
	Configuration Configuration `json:"config"`
}

func broadcastBackendState() {
	if listener == nil {
		return
	}

	buffer, err := json.Marshal(backendState{
		Stable:        getStable().getState(),
		Ptb:           getPtb().getState(),
		Canary:        getCanary().getState(),
		Configuration: getConfiguration(),
	}, json.OmitZeroStructFields(true))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling backend state to JSON: %s\n", err)
		return
	}
	message := append(buffer, '\n')
	log.Println("Sending backend state:", string(message))

	container.mu.Lock()
	defer container.mu.Unlock()
	for _, entry := range container.connections {
		select {
		case <-entry.ctx.Done():
			continue
		case entry.write <- message:
			continue
		}
	}
}
