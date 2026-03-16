// Dear liberals,
// ... this file is called "Socket.vala",
// yet 41% of it deals with JSON parsing.
// Curious!

public struct ReleaseInternal {
	string install_path;
	DateTime last_checked;
	string latest_version;
	string command_line_arguments;
	bool bd_enabled;
	string bd_channel;
}

public struct ReleaseProcess {
	string status;
	string message;
	uint8 progress;
	string error;
}

public struct ReleaseState {
	ReleaseInternal? internal;
	string version;
	ReleaseProcess? process;
}

public struct Configuration {
	bool automatically_check_for_updates;
	bool notify_on_update_available;
	bool automatically_install_updates;
	string default_install_path;
}

public struct BackendState {
	ReleaseState? stable;
	ReleaseState? ptb;
	ReleaseState? canary;
	Configuration config;
}

public struct SocketState {
	BackendState backend_state;
	string? waiting;
	Error error;
	Error critical;
}

public errordomain SocketError {
	NOT_CONNECTED,
	DAEMON_NOT_FOUND,
	DAEMON_ERROR,
	INVALID_RESPONSE
}

class Socket {
	public delegate void UpdateStateCallback (Socket socket, SocketState state);

	private static Socket _instance;
	public static Socket instance {
		get {
			if (_instance == null)
				_instance = new Socket ();
			return _instance;
		}
	}
	private static Queue<string> commands;

	public static SocketState get_state () {
		return instance.state;
	}

	public static void start () {
		new Thread<void> ("start", instance.connect);
	}

	public static void command (string command) {
		if (commands == null)
			commands = new Queue<string> ();

		commands.push_tail (command);

		if (instance.connection == null)
			return;

		while (!commands.is_empty ())
			try {
				var next_command = commands.pop_head ();
				instance.connection.output_stream.write (next_command.data);
				instance.connection.output_stream.write ("\n".data);
				instance.connection.output_stream.flush ();
			} catch (Error e) {
				// todo distinguish between temporary and critical write errors
				// e.g. connection closed or refused should be critical whereas i'm sure some errors may be recoverable/retryable
				// for now, just immediately do critical to fail loudly
				instance.critical = e;
			}
	}

	private SocketConnection? _connection;
	private SocketConnection? connection {
		get {
			return _connection;
		}
		set {
			_connection = value;
			if (value == null)
				critical = new SocketError.NOT_CONNECTED ("No connection to backend socket");
		}
	}
	private Thread<void> reader;
	private bool should_read;
	private SocketState state;
	private string waiting {
		get {
			return state.waiting;
		}
		set {
			if (value != null)
				info (value);

			lock (state) {
				state.waiting = value;
				state_sig (state);
			}
		}
	}
	// Errors are temporary and recoverable
	private Error? error {
		get {
			return state.error;
		}
		set {
			lock (state) {
				state.error = value;
				state_sig (state);
			}

			if (value != null)
				stderr.printf ("Socket ERROR: %s\n", value.message);
		}
	}
	// Critical states are permanent and at least will require a restart
	private Error? critical {
		get {
			return state.critical;
		}
		set {
			lock (state) {
				state.critical = value;
				state_sig (state);
			}

			if (value != null)
				stderr.printf ("Socket CRITICAL: %s\n", value.message);
		}
	}

	public signal void state_sig (SocketState state);

	private Socket () {}

	private Value parse_value (Json.Node node, GLib.Type type) throws SocketError {
		if (node.get_node_type () != Json.NodeType.VALUE)
			throw new SocketError.INVALID_RESPONSE ("not a value: %d", node.get_node_type ());
		if (node.get_value_type () != type)
			throw new SocketError.INVALID_RESPONSE ("invalid value type: %s", node.get_value_type ().name ());

		return node.get_value ();
	}

	private void parse_release (Json.Node release, ref ReleaseState? state) throws SocketError {
		if (release.get_node_type () == Json.NodeType.NULL) {
			state = null;
			return;
		}
		state = {};

		var object = release.get_object ();

		var release_internal = object.get_member ("internal");
		if (release_internal.get_node_type () == Json.NodeType.OBJECT) {
			state.internal = {};
			var internal_object = release_internal.get_object ();
			// try {
			state.internal.install_path = parse_value (internal_object.get_member ("install_path"), Type.STRING).get_string ();
			var last_checked = parse_value (internal_object.get_member ("last_checked"), Type.STRING).get_string ();
			state.internal.last_checked = new DateTime.from_iso8601 (last_checked, null);
			if (state.internal.last_checked == null)
				throw new SocketError.INVALID_RESPONSE ("`last_checked` is not a valid DateTime: %s", last_checked);
			state.internal.latest_version = parse_value (internal_object.get_member ("latest_version"), Type.STRING).get_string ();
			state.internal.command_line_arguments = parse_value (internal_object.get_member ("command_line_arguments"), Type.STRING).get_string ();
			state.internal.bd_enabled = parse_value (internal_object.get_member ("bd_enabled"), Type.BOOLEAN).get_boolean ();
			var bd_channel = parse_value (internal_object.get_member ("bd_channel"), Type.STRING).get_string ();
			if (bd_channel != "stable" && bd_channel != "canary")
				throw new SocketError.INVALID_RESPONSE ("invalid BetterDiscord channel: %s", bd_channel);
			state.internal.bd_channel = bd_channel;
			// } catch (Error e) {
			// critical = e;
			// }
		} else if (release_internal.get_node_type () == Json.NodeType.NULL)
			state.internal = null;
		else
			throw new SocketError.INVALID_RESPONSE ("invalid internal node type: %d", release_internal.get_node_type ());

		// try {
		state.version = parse_value (object.get_member ("version"), Type.STRING).get_string ();
		// } catch (Error e) {
		// critical = e;
		// }

		var process = object.get_member ("process");
		if (process.get_node_type () == Json.NodeType.OBJECT) {
			state.process = {};
			var process_object = process.get_object ();
			// try {
			state.process.status = parse_value (process_object.get_member ("status"), Type.STRING).get_string ();
			state.process.message = parse_value (process_object.get_member ("message"), Type.STRING).get_string ();
			var progress = parse_value (process_object.get_member ("progress"), Type.INT64).get_int64 ();
			if (progress < uint8.MIN || progress > uint8.MAX)
				throw new SocketError.INVALID_RESPONSE ("`progress` is not a valid uint8");
			state.process.progress = (uint8) progress;
			state.process.error = parse_value (process_object.get_member ("error"), Type.STRING).get_string ();
			// } catch (Error e) {
			// critical = e;
			// }
		} else if (process.get_node_type () == Json.NodeType.NULL)
			state.process = null;
		else
			throw new SocketError.INVALID_RESPONSE ("invalid process node type: %d", process.get_node_type ());
	}

	private void handle_message (string message) {
		var parser = new Json.Parser ();
		try {
			parser.load_from_data (message);
			var root = parser.get_root ();
			if (root.get_node_type () != Json.NodeType.OBJECT)
				throw new SocketError.INVALID_RESPONSE ("invalid root type: %d", root.get_node_type ());

			var root_object = root.get_object ();
			BackendState backend_state = {};

			parse_release (root_object.get_member ("stable"), ref backend_state.stable);
			parse_release (root_object.get_member ("ptb"), ref backend_state.ptb);
			parse_release (root_object.get_member ("canary"), ref backend_state.canary);

			var config = root_object.get_member ("config");
			if (config.get_node_type () != Json.NodeType.OBJECT)
				throw new SocketError.INVALID_RESPONSE ("invalid config node type: %d", config.get_node_type ());

			backend_state.config = {};
			var config_object = config.get_object ();
			backend_state.config.automatically_check_for_updates = parse_value (config_object.get_member ("automatically_check_for_updates"), Type.BOOLEAN).get_boolean ();
			backend_state.config.notify_on_update_available = parse_value (config_object.get_member ("notify_on_update_available"), Type.BOOLEAN).get_boolean ();
			backend_state.config.automatically_install_updates = parse_value (config_object.get_member ("automatically_install_updates"), Type.BOOLEAN).get_boolean ();
			backend_state.config.default_install_path = parse_value (config_object.get_member ("default_install_path"), Type.STRING).get_string ();

			lock (state) {
				state.backend_state = backend_state;
				state_sig (state);
			}
		} catch (Error e) {
			critical = e;
		}
	}

	private void read () {
		// `Json.Parser.load_from_stream` is unsuitable because it reads (and thereby blocks) until EOF, and we need to keep the connection open persistently
		var stream = new DataInputStream (connection.input_stream);
		waiting = null;
		while (should_read) {
			try {
				var message = stream.read_line_utf8 ();
				if (message == null) {
					connection = null;
					break;
				}
				stdout.printf ("Received message: %s\n", message);
				handle_message (message);
			} catch (IOError e) {
				critical = e;
			}
		}
		reader = null;
	}

	private void connect () {
		waiting = "Starting socket client";
		error = null;
		critical = null;
		if (connection != null) {
			try {
				waiting = "Closing pre-existing connection";
				connection.close ();
			} catch (IOError e) {
				critical = e;
			}
			connection = null;
		}
		if (reader != null) {
			waiting = "Waiting on pre-existing reader to exit";
			should_read = false;
			reader.join ();
			reader = null;
		}

		waiting = "Getting path from daemon";
		var path = "";
		try {
			var err = "";
			Process.spawn_command_line_sync ("dislaunchd path", out path, out err);
			if (err != "") {
				critical = new SocketError.DAEMON_ERROR ("daemon encountered error while getting path: %s", err);
				return;
			}
		} catch (SpawnError e) {
			critical = e;
			return;
		}
		if (path == "") {
			critical = new SocketError.DAEMON_ERROR ("daemon returned no path");
			return;
		}
		info ("Received path '%s' from daemon".printf (path));

		var client = new SocketClient () {
			family = SocketFamily.UNIX,
			type = SocketType.STREAM
		};

		try {
			waiting = "Connecting to socket";
			connection = client.connect (new UnixSocketAddress (path));
		} catch (Error e) {
			critical = e;
			return;
		}

		waiting = "Spawning reader";
		should_read = true;
		reader = new Thread<void> ("read", read);
		waiting = null;

		command ("state");
	}
}