void usage (string name) {
	stdout.printf ("%s - Send commands to the Dislaunch daemon\n\n", name);
	stdout.printf ("%s {stable|ptb|canary} <command>\n", name);
	stdout.printf ("command:\n");
	stdout.printf ("\tbd_channel {stable|canary} - Sets the BetterDiscord release channel to use when BetterDiscord is enabled.\n");
	stdout.printf ("\tbd_enabled {0|1} - Sets whether Dislaunch should inject BetterDiscord.\n");
	stdout.printf ("\tcheck_for_updates - Check whether any updates to Discord and BetterDiscord are available. Does not by itself install updates.\n");
	stdout.printf ("\tcommand_line_arguments <args> - Sets the command-line arguments Dislaunch should execute Discord with.\n");
	stdout.printf ("\tinstall - Installs the latest version of Discord. If it is already installed, update it if any update is available (check_for_update must be run first.)\n");
	stdout.printf ("\tmove <path> - Move the path in which Discord is installed to <path>.\n");
	stdout.printf ("\tuninstall - Uninstalls this release of Discord.\n\n");
	stdout.printf ("%s config <command>\n", name);
	stdout.printf ("command:\n");
	stdout.printf ("\tautomatically_check_for_updates {0|1} - Sets whether the daemon should automatically run check_for_updates on each installed release on an interval.\n");
	stdout.printf ("\tnotify_on_update_available {0|1} - Send a notification if an update is available. Has no effect when automatically_check_for_updates is disabled.\n");
	stdout.printf ("\tautomatically_install_updates {0|1} - Automatically update Discord when an update is available. Has no effect when automatically_check_for_updates is disabled.\n");
	stdout.printf ("\tdefault_install_path <path> - Sets the default path to which Dislaunch should install new releases of Discord. Has no effect on already installed releases - those must be moved with their respective move command.\n");
}

int main (string[] args) {
	if (args.length <= 1) {
		usage (args[0]);
		return Posix.EXIT_SUCCESS;
	}

	// HACK the CLI basically just mirrors the socket API
	// TODO stop doing this hack and implement a real CLI
	Socket.start ();
	Thread.usleep (50000);
	Socket.command (string.joinv (" ", args[1 :]));

	return Posix.EXIT_SUCCESS;
}