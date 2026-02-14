int launch (ReleaseChannel channel) {
	Socket.start ();
	ReleaseState? state = channel.to_state (Socket.get_state ().global_state);

	// if (state == null) {
	// stderr.printf ("Release '%s' is not installed. Please install it first.\n", channel.id);
	// return 1;
	// }

	Socket.command (channel.id + " check_for_updates");
	// if (state.version != state.internal.latest_version) {
	Progress progress = new Progress (channel);
	progress.run ();
	// }

	string path_name = channel.title;
	if (channel == ReleaseChannel.PTB)
		path_name = "DiscordPTB";
	else if (channel == ReleaseChannel.CANARY)
		path_name = "DiscordCanary";
	Posix.execvp ("%s/%s/%s".printf (state.internal.install_path, path_name, path_name), {}); // TODO add support for custom command-line arguments! be a real launcher!
	stderr.printf ("Failed to launch " + channel.title + ": %s\n", strerror (errno));
	return 1;
}

int main (string[] args) {
	if (!Thread.supported ()) {
		stderr.printf ("Cannot run without thread support\n");
		return 1;
	}

	if (args.length == 1) {
		Application application = new Application ();
		return application.run (args);
	}

	switch (args[1]) {
	case "stable" :
		return launch (ReleaseChannel.STABLE);
	case "ptb":
		return launch (ReleaseChannel.PTB);
	case "canary":
		return launch (ReleaseChannel.CANARY);
	default:
		stderr.printf ("Unknown argument: %s\n", args[1]);
		return 1;
	}
}
