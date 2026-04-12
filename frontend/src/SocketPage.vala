class SocketPage : Adw.Bin {
private Adw.StatusPage status_page;
private Adw.SpinnerPaintable spinner_paintable;
private Gtk.Button reset_button;

public SocketPage () {
	status_page = new Adw.StatusPage () {
		halign = Gtk.Align.FILL,
		hexpand = true,
		vexpand = true
	};
	child = status_page;

	spinner_paintable = new Adw.SpinnerPaintable (status_page);

	reset_button = new Gtk.Button () {
		label = "Reset Connection",
		halign = Gtk.Align.CENTER,
		has_frame = true
	};
	reset_button.set_size_request (100, 40);
	reset_button.clicked.connect (() => Socket.start ());

	Socket.on_state (refresh);
	Socket.command ("state");
}

private void refresh (SocketState state) {
	if (state.critical != null) {
		status_page.title = "Socket Error";
		status_page.icon_name = "dialog-error";

		var comment = "";
		if (state.critical is SocketError) {
			switch (state.critical.code) {
			case SocketError.DAEMON_NOT_FOUND:
				comment = "Is the daemon installed?";
				break;
			case SocketError.DAEMON_ERROR:
			case SocketError.INVALID_RESPONSE:
				comment = "Is the daemon up to date?";
				break;
			case SocketError.NOT_CONNECTED:
				comment = "You may try connecting again.";
				break;
			}
		} else if (state.critical is IOError) {
			switch (state.critical.code) {
			case IOError.NOT_FOUND:
				comment = "Is the daemon running?";
				break;
			}
		}

		status_page.description = comment ==
			"" ? "Dislaunch encountered an error connecting to the backend: " +
			state.critical.message :
			"Dislaunch encountered an error connecting to the backend: %s\n\n%s".printf (
			state.critical.message, comment);
		status_page.child = reset_button;
	} else if (state.waiting != null) {
		status_page.title = "Waiting…";
		status_page.paintable = spinner_paintable;
		status_page.description = state.waiting + "…";
		status_page.child = null;
	}
}
}