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
	int64? bd_installed_release;
	int64? bd_latest_release;
}

public struct ReleaseState {
	string status;
	string message;
	uint8 progress;
	string error;

	ReleaseInternal? internal;
	string version;
}

public struct Configuration {
	bool automatically_check_for_updates;
	bool notify_on_update_available;
	bool automatically_install_updates;
	string default_install_path;
}

public struct BackendState {
	ReleaseState stable;
	ReleaseState ptb;
	ReleaseState canary;
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
public delegate void UpdateStateCallback (SocketState state);

private static Socket _instance;
private static Socket instance {
	get {
		if (_instance == null)
			_instance = new Socket ();
		return _instance;
	}
}
private static Queue<string> commands;

public static SocketState get_state () {
	SocketState? response = null;

	ulong id = 0; // HACK `id` needs to have been declared in the handler
	id = instance.state_sig.connect (
		(_, state) => {
			response = state;
			SignalHandler.disconnect (instance, id);
		}
	);

	command ("state");
	while (response == null) ;
	return response;
}

public static void on_state (UpdateStateCallback callback) {
	instance.state_sig.connect (
		(_, state) => Idle.add (
			() => {
			callback (state);
			return Source.REMOVE;
		}
		)
	);
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
private Cancellable cancellable;
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

private signal void state_sig (SocketState state);

private Socket () {
}

private Value parse_value (Json.Object object, string member, Type type) throws SocketError {
	if (!object.has_member (member)) {
		var value = Value (type);
		switch (type) {
		case Type.BOOLEAN :
			value.set_boolean (false);
			break;
		case Type.INT64 :
			value.set_int64 (0);
			break;
		case Type.STRING :
			value.set_string ("");
			break;
			default :
			// unimplemented
			assert_not_reached ();
		}
		return value;
	}

	var node = object.get_member (member);
	if (node.get_node_type () != Json.NodeType.VALUE)
		throw new SocketError.INVALID_RESPONSE ("not a value: %d", node.get_node_type ());
	if (node.get_value_type () != type)
		throw new SocketError.INVALID_RESPONSE ("invalid value type: %s", node.get_value_type ().name ());

	return node.get_value ();
}

private void parse_release (Json.Object parent_object, string channel, out ReleaseState state) throws SocketError {
	if (!parent_object.has_member (channel))
		throw new SocketError.INVALID_RESPONSE ("release '%s' is absent", channel);

	var release = parent_object.get_member (channel);
	if (release.get_node_type () != Json.NodeType.OBJECT)
		throw new SocketError.INVALID_RESPONSE ("release '%s' is not an object", channel);

	state = {};

	var object = release.get_object ();

	// try {
	state.status = parse_value (object, "status", Type.STRING).get_string ();
	state.message = parse_value (object, "message", Type.STRING).get_string ();
	var progress = parse_value (object, "progress", Type.INT64).get_int64 ();
	if (progress < uint8.MIN || progress > uint8.MAX)
		throw new SocketError.INVALID_RESPONSE ("`progress` is not a valid uint8");
	state.progress = (uint8) progress;
	state.error = parse_value (object, "error", Type.STRING).get_string ();
	// } catch (Error e) {
	// critical = e;
	// }

	// try {
	state.version = parse_value (object, "version", Type.STRING).get_string ();
	// } catch (Error e) {
	// critical = e;
	// }

	if (!object.has_member ("internal")) {
		state.internal = null;
		return;
	}


	var release_internal = object.get_member ("internal");

	if (release_internal.get_node_type () != Json.NodeType.OBJECT)
		throw new SocketError.INVALID_RESPONSE (
			"invalid internal node type: %d",
			release_internal.get_node_type ()
		);

	state.internal = {};
	var internal_object = release_internal.get_object ();
	// try {
	state.internal.install_path = parse_value (internal_object, "install_path", Type.STRING).get_string ();
	var last_checked = parse_value (internal_object, "last_checked", Type.STRING).get_string ();
	state.internal.last_checked = new DateTime.from_iso8601 (last_checked, null);
	if (last_checked != "" && state.internal.last_checked == null)
		throw new SocketError.INVALID_RESPONSE ("`last_checked` is not a valid DateTime: %s", last_checked);
	state.internal.latest_version = parse_value (internal_object, "latest_version", Type.STRING).get_string ();
	state.internal.command_line_arguments = parse_value (
		internal_object, "command_line_arguments",
		Type.STRING
	).get_string ();
	state.internal.bd_enabled = parse_value (internal_object, "bd_enabled", Type.BOOLEAN).get_boolean ();
	var bd_channel = parse_value (internal_object, "bd_channel", Type.STRING).get_string ();
	if (bd_channel != "stable" && bd_channel != "canary")
		throw new SocketError.INVALID_RESPONSE ("invalid BetterDiscord channel: %s", bd_channel);
	state.internal.bd_channel = bd_channel;
	state.internal.bd_installed_release = parse_value (
		internal_object, "bd_installed_release",
		Type.INT64
	).get_int64 ();
	state.internal.bd_latest_release = parse_value (internal_object, "bd_latest_release", Type.INT64).get_int64 ();
	// } catch (Error e) {
	// critical = e;
	// }
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

		parse_release (root_object, "stable", out backend_state.stable);
		parse_release (root_object, "ptb", out backend_state.ptb);
		parse_release (root_object, "canary", out backend_state.canary);

		if (root_object.has_member ("config")) {
			var config = root_object.get_member ("config");
			if (config.get_node_type () != Json.NodeType.OBJECT)
				throw new SocketError.INVALID_RESPONSE (
					"invalid config node type: %d",
					config.get_node_type ()
				);

			backend_state.config = {};
			var config_object = config.get_object ();
			backend_state.config.automatically_check_for_updates = parse_value (
				config_object,
				"automatically_check_for_updates", Type.BOOLEAN
			).get_boolean ();
			backend_state.config.notify_on_update_available = parse_value (
				config_object,
				"notify_on_update_available", Type.BOOLEAN
			).get_boolean ();
			backend_state.config.automatically_install_updates = parse_value (
				config_object,
				"automatically_install_updates", Type.BOOLEAN
			).get_boolean ();
			backend_state.config.default_install_path = parse_value (
				config_object, "default_install_path",
				Type.STRING
			).get_string ();
		}

		lock (state) {
			state.backend_state = backend_state;
			state_sig (state);
		}
	} catch (Error e) {
		critical = e;
	}
}

private void read (Cancellable cancellable) {
	// `Json.Parser.load_from_stream` is unsuitable because it reads (and thereby blocks) until EOF, and we need to keep the connection open persistently
	var stream = new DataInputStream (connection.input_stream);
	waiting = null;
	while (!cancellable.is_cancelled ()) {
		try {
			var message = stream.read_line_utf8 (null, cancellable);
			if (message == null) {
				connection = null;
				break;
			}
			stdout.printf ("Received message: %s\n", message);
			handle_message (message);
		} catch (IOError e) {
			if (e.code != IOError.CANCELLED)
				critical = e;
		}
	}
}

private void connect () {
	cancellable.cancel ();
	waiting = "Starting socket client";
	error = null;
	critical = null;
	if (reader != null) {
		waiting = "Waiting on pre-existing reader to exit";
		reader.join ();
		reader = null;
	}
	if (connection != null) {
		try {
			waiting = "Closing pre-existing connection";
			connection.close ();
		} catch (IOError e) {
			critical = e;
		}
		connection = null;
	}

	waiting = "Getting path from daemon";
	var path = "";
	try {
		var err = "";
		Process.spawn_command_line_sync ("dislaunchd path", out path, out err);
		if (err != "")
			throw new SocketError.DAEMON_ERROR ("daemon encountered error while getting path: %s", err);
	} catch (Error e) {
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
	cancellable = new Cancellable ();
	reader = new Thread<void> ("read", () => read (cancellable));
	waiting = null;

	command ("state");
}
}