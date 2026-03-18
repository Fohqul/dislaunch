int launch (ReleaseChannel channel) {
	Socket.start ();
	ReleaseState? state = channel.to_state (Socket.get_state ().backend_state);

	// if (state == null) {
	// stderr.printf ("Release '%s' is not installed. Please install it first.\n", channel.id);
	// return Posix.EXIT_FAILURE;
	// }

	Socket.command (channel.id + " check_for_updates");
	// if (state.version != state.internal.latest_version) {
	new Progress (channel).run ();
	// }

	string path_name = channel.title;
	if (channel == ReleaseChannel.PTB)
		path_name = "DiscordPTB";
	else if (channel == ReleaseChannel.CANARY)
		path_name = "DiscordCanary";

	var executable = "%s/%s/%s".printf (state.internal.install_path, path_name, path_name);

	string[] command_line_arguments;
	try {
		Shell.parse_argv (state.internal.command_line_arguments, out command_line_arguments);
	} catch (ShellError e) {
		stderr.printf ("Failed to parse command-line arguments '%s': %s\n", state.internal.command_line_arguments, e.message);
		command_line_arguments = {};
	}

	var argv = new string[command_line_arguments.length + 1];
	argv[0] = executable;
	for (int i = 0; i < command_line_arguments.length; ++i)
		argv[i + 1] = command_line_arguments[i];

	Posix.execv (executable, argv);

	stderr.printf ("Failed to launch " + channel.title + ": %s\n", strerror (errno));
	return Posix.EXIT_FAILURE;
}

int main (string[] args) {
	if (!Thread.supported ()) {
		stderr.printf ("Cannot run without thread support\n");
		return Posix.EXIT_FAILURE;
	}

	if (args.length == 1)
		return new Application ().run (args);

	switch (args[1]) {
	case "stable" :
		return launch (ReleaseChannel.STABLE);
	case "ptb":
		return launch (ReleaseChannel.PTB);
	case "canary":
		return launch (ReleaseChannel.CANARY);
	default:
		stderr.printf ("Unknown argument: %s\n", args[1]);
		return Posix.EXIT_FAILURE;
	}
}