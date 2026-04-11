class Progress : Adw.Application {
	private static string css = """
	.container {
		padding: 2em;
	}

	.container > label {
		font-size: 2em;
		font-weight: bold;
		margin-bottom: 1em;
	}

	.spinner {
		margin: 1em;
	}

	.message {
		font-size: 1.8em;
		font-weight: bold;
	}

	listview {
		background: transparent;
	}

	listview label {
		transition: all 0.1s ease-out;
		font-size: 1.1em;
		padding: 0.2em;
	}
	""";

	public ReleaseChannel channel;
	private Adw.ViewStack view_stack;
	private Gtk.Label status;
	private Gtk.ListView list_view;
	private Gtk.Label message;
	private ProgressBar progress_bar;
	private ListStore messages;
	private string last_status = "";
	private string last_message = "";

	// last `error` value regardless of whether it's empty - used to determine whether there's a new error to append to `messages`
	private string last_error = "";
	// last `error` value that wasn't empty - used by `refresh` to determine whether an error happened at all during the previous operation, even if current state has no error
	private string? last_error_present = null;

	private bool checked = false;
	private uint8 attempts_remaining = 4;

	public Progress (ReleaseChannel channel) {
		Object (
		        application_id: "io.github.Fohqul.Dislaunch.Progress." + channel.id,
		        flags: ApplicationFlags.DEFAULT_FLAGS
		);
		this.channel = channel;
	}

	public override void activate () {
		Css.add (css);

		var application_window = new Adw.ApplicationWindow (this) {
			title = "Dislaunch: " + channel.title + " Progress",
			default_height = 450,
			default_width = 700,
			resizable = false
		};

		view_stack = new Adw.ViewStack ();
		application_window.content = view_stack;

		view_stack.add_named (SuspensePageFactory.create ("Launching " + channel.title + "…"), "suspense");

		view_stack.add_named (new SocketPage (), "socket");

		view_stack.add_named (new InstallPage (channel), "install");

		var container = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
		container.add_css_class ("container");
		view_stack.add_named (container, "process");

		status = new Gtk.Label ("Starting") { halign = Gtk.Align.START };
		container.append (status);

		var scrolled_window = new Gtk.ScrolledWindow () { vexpand = true };
		scrolled_window.vadjustment.notify["upper"].connect (() => {
			// todo don't scroll if user's scrolled up intentionally/manually
			list_view.scroll_to (messages.n_items - 1, Gtk.ListScrollFlags.NONE, null);
		});
		container.append (scrolled_window);

		messages = new ListStore (Type.OBJECT);
		messages.items_changed.connect (() => {
			list_view.scroll_to (messages.n_items - 1, Gtk.ListScrollFlags.NONE, null);
		});

		var signal_list_item_factory = new Gtk.SignalListItemFactory ();
		signal_list_item_factory.bind.connect ((_, object) => {
			Gtk.ListItem? list_item = object as Gtk.ListItem;
			assert_nonnull (list_item);
			Gtk.StringObject message = list_item.item as Gtk.StringObject;
			assert_nonnull (message);

			Gtk.Label label = new Gtk.Label (message.string) { halign = Gtk.Align.START, valign = Gtk.Align.END, wrap = true, wrap_mode = Pango.WrapMode.WORD_CHAR };
			if (message.string.has_prefix ("Error: "))
				label.add_css_class ("error");
			list_item.child = label;
		});

		list_view = new Gtk.ListView (new Gtk.NoSelection (messages), signal_list_item_factory);
		scrolled_window.child = list_view;

		var message_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 0);
		container.append (message_box);

		message_box.append (new Adw.Spinner () { height_request = 50, width_request = 50 });

		message = new Gtk.Label ("Starting") { wrap = true, wrap_mode = Pango.WrapMode.WORD_CHAR };
		message.add_css_class ("message");
		message_box.append (message);

		progress_bar = new ProgressBar ();
		container.append (progress_bar);

		view_stack.visible_child_name = "suspense";
		application_window.present ();
		Socket.on_state (refresh);
		Socket.start ();
	}

	private void append_message (string message) {
		messages.append (new Gtk.StringObject (message));
		list_view.scroll_to (messages.n_items - 1, Gtk.ListScrollFlags.NONE, null);
	}

	private void refresh (SocketState state) {
		if (state.critical != null || state.waiting != null) {
			view_stack.visible_child_name = "socket";
			return;
		}

		var release_state = channel.to_state (state.backend_state);

		if (release_state == null || release_state.version == "" || release_state.internal == null) {
			view_stack.visible_child_name = "install";
			return;
		}

		view_stack.visible_child_name = "process";

		switch (release_state.status) {
		case "" :
			if (last_status == "update_check") {
				if (release_state.version != release_state.internal.latest_version) {
					attempts_remaining = 4; // so that `install` may reuse this field
					channel.command ("install");
					append_message ("An update is available to " + release_state.internal.latest_version);
				} else if (last_error_present != null) {
					if (attempts_remaining <= 0) {
						// TODO inhibit and have user explicitly launch on error - otherwise there's no indication anything went wrong
						stdout.printf ("Exhausted update check attempts - launching anyway\n");
						quit ();
						return;
					}

					--attempts_remaining;
					channel.command ("check_for_updates");
					checked = true;
					append_message ("Last attempt to check for updates failed - %d remaining".printf (attempts_remaining));
					last_error_present = null; // reset so that later iteration can know whether an error happened during its last attempt
				} else {
					stdout.printf ("Up to date - quitting\n");
					quit ();
					return;
				}
			} else if (last_status == "install") {
				if (release_state.version == release_state.internal.latest_version) {
					stdout.printf ("Installed latest version - quitting\n");
					quit ();
					return;
				}

				if (attempts_remaining <= 0) {
					// TODO inhibit
					stdout.printf ("Exhausted installation attempts - launching anyway\n");
					quit ();
					return;
				}

				--attempts_remaining;
				channel.command ("install");
				append_message ("Last attempt to install failed - %d remaining".printf (attempts_remaining));
			} else {
				if (!checked) {
					channel.command ("check_for_updates");
					checked = true;
				}
				status.label = "Starting";
			}
			break;
		case "download":
			status.label = "Downloading";
			break;
		case "install":
			status.label = "Installing";
			break;
		case "update_check":
			// don't flash `process` page if up to date
			if (attempts_remaining == 4)
				view_stack.visible_child_name = "suspense";
			status.label = "Checking for updates";
			break;
		case "bd_injection":
			status.label = "Injecting BetterDiscord";
			break;
		case "move":
			status.label = "Moving";
			break;
		case "uninstall":
			status.label = "Uninstalling";
			break;
		case "fatal":
			view_stack.visible_child_name = "fatal";
			break;
		default:
			stderr.printf ("Unrecognised status: %s\n", release_state.status);
			break;
		}
		last_status = release_state.status;

		progress_bar.progress = release_state.progress;

		message.label = release_state.message;

		if (release_state.message != last_message) {
			last_message = release_state.message;
			if (release_state.message != "")
				append_message (release_state.message);
		}

		if (release_state.error != last_error) {
			last_error = release_state.error;
			if (release_state.error != "") {
				append_message ("Error: " + release_state.error);
				last_error_present = release_state.error;
			}
		}

		if (messages.n_items == 0)
			view_stack.visible_child_name = "suspense";
	}
}