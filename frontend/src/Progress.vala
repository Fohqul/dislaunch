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

	listview label {
		transition: all 0.1s ease-out;
		font-size: 1.1em;
		padding: 0.2em;
	}

	listview label:not(.error) {
		color: grey;
	}

	@keyframes move-up {
		from {
			transform: translateY(55px);
		}
		to {
			transform: translateY(0);
		}
	}

	listview row {
		transition: all 0.1s ease-out;
		/*animation-name: move-up;
		animation-fill-mode: forwards;
		animation-duration: 0.1s;*/
	}

	@keyframes unfocus {
		from {
			transform: translateY(50px);
		}
		to {
			transform: translateY(0);
		}
	}

	listview row:nth-last-child(2) {
		animation-name: unfocus;
		animation-fill-mode: forwards;
		animation-duration: 0.1s;
	}

	listview row:last-child {
		animation-name: appear;
		animation-fill-mode: forwards;
		animation-duration: 0.1s;
	}

	listview row:last-child label {
		transition: none;
		color: black;
		font-size: 1.8em;
		font-weight: bold;
		padding: 0.4em;
	}

	@keyframes appear {
		from {
			transform: translateY(50px);
		}
		to {
			transform: translateY(0);
		}
	}
	""";

	public ReleaseChannel channel;
	private Adw.ViewStack view_stack;
	private Gtk.Label status;
	private Gtk.ListView list_view;
	private ProgressBar progress_bar;
	private ListStore messages;
	private string? last_error;

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
			default_height = 350,
			default_width = 550,
			resizable = false
		};

		view_stack = new Adw.ViewStack ();
		application_window.content = view_stack;

		view_stack.add_named (SuspensePageFactory.create ("Launching…"), "suspense");

		view_stack.add_named (new InstallPage (channel), "install");

		var container = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
		container.add_css_class ("container");
		view_stack.add_named (container, "process");

		status = new Gtk.Label ("Starting") { halign = Gtk.Align.START };
		container.append (status);

		var scrolled_window = new Gtk.ScrolledWindow () { vexpand = true };
		scrolled_window.vadjustment.notify["upper"].connect ((object, _) => {
			// todo don't scroll if user's scrolled up intentionally/manually
			list_view.scroll_to (messages.n_items - 1, Gtk.ListScrollFlags.NONE, null);
		});
		container.append (scrolled_window);

		messages = new ListStore (Type.OBJECT);

		var signal_list_item_factory = new Gtk.SignalListItemFactory ();
		signal_list_item_factory.bind.connect ((_, object) => {
			Gtk.ListItem? list_item = object as Gtk.ListItem;
			assert_nonnull (list_item);
			Gtk.StringObject message = list_item.item as Gtk.StringObject;
			assert_nonnull (message);

			Gtk.Label label = new Gtk.Label (message.string) { halign = Gtk.Align.START, valign = Gtk.Align.END };
			if (message.string.has_prefix ("Error: "))
				label.add_css_class ("error");
			list_item.child = label;
		});

		list_view = new Gtk.ListView (new Gtk.NoSelection (messages), signal_list_item_factory) { valign = Gtk.Align.END };
		scrolled_window.child = list_view;

		progress_bar = new ProgressBar ();
		container.append (progress_bar);

		refresh (ReleaseState () { process = ReleaseProcess () { progress = 65, message = "0" }, internal = ReleaseInternal () {} });
		view_stack.visible_child_name = "suspense";
		application_window.present ();
		// Socket.instance.state_sig.connect ((_, state) => refresh (channel.to_state (state.backend_state)));
		new Thread<void> ("sid", () => {
			for (uint8 i = 95; i < uint8.MAX; i++) {
				Idle.add (() => { refresh (ReleaseState () { internal = ReleaseInternal () {}, process = ReleaseProcess () { progress = i, message = "%u".printf (i) } }); return Source.CONTINUE; });
				Thread.usleep (600000);
			}
		});
	}

	private bool should_append_message (string message) {
		if (messages.n_items == 0)
			return true;

		var last_message = messages.get_object (messages.n_items - 1) as Gtk.StringObject;
		assert_nonnull (last_message);
		return message != last_message.string;
	}

	private void refresh (ReleaseState? state) {
		if (state == null) {
			view_stack.visible_child_name = "install";
			return;
		}

		view_stack.visible_child_name = "process";

		switch (state.process.status) {
		case null :
		case "" :
			status.label = "Starting";
			break;
		case "download":
			status.label = "Downloading";
			break;
		case "install":
			status.label = "Installing";
			break;
		case "update_check":
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
			stderr.printf ("Unrecognised status: %s\n", state.process.status);
			break;
		}

		progress_bar.progress = state.process.progress;

		if (should_append_message (state.process.message))
			messages.append (new Gtk.StringObject (state.process.message));


		if (state.process.error != last_error) {
			last_error = state.process.error;
			if (state.process.error != null && state.process.error != "")
				messages.insert (messages.n_items == 0 ? 0 : messages.n_items - 1, new Gtk.StringObject ("Error: " + state.process.error));
		}
	}
}