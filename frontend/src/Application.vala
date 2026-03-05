class Application : Adw.Application {
	private static string css = """
	.alert {
		background-color: alpha(var(--error-color), 0.3);
		margin: 0.8em;
		border: 1px solid var(--error-bg-color);
		border-radius: 8px;
	}

	.alert-title {
		background-color: var(--error-bg-color);
		color: var(--error-fg-color);
		font-weight: bold;
		border: 2px solid var(--error-bg-color);
		padding-left: 0.6em;
		border-radius: 6px 6px 0 0;
	}

	.alert-title label {
		line-height: 1.0;
	}

	.alert-title button > image {
		color: var(--error-fg-color);
	}

	.alert > label {
		margin: 0.6em;
	}
	""";

	private Adw.ApplicationWindow application_window;
	private Adw.HeaderBar header_bar;
	private Gtk.MenuButton menu_button;
	private Adw.PreferencesDialog configuration_dialogue;
	private Adw.ViewStack view_stack;
	private Adw.StatusPage waiting_page;
	private Adw.StatusPage critical_page;
	private Gtk.Revealer release_alert_revealer;
	private Gtk.Label release_alert_label;

	public Application () {
		Object (application_id: "io.github.Fohqul.Dislaunch", flags: ApplicationFlags.DEFAULT_FLAGS);
	}

	public override void activate () {
		Css.add (css);

		application_window = new Adw.ApplicationWindow (this) {
			title = "Dislaunch",
			default_height = 600,
			default_width = 450,
			resizable = false
		};

		var toolbar_view = new Adw.ToolbarView ();
		application_window.content = toolbar_view;

		header_bar = new Adw.HeaderBar () {
			// Show both and the theme will decide which to use
			show_start_title_buttons = true,
			show_end_title_buttons = true
		};
		toolbar_view.add_top_bar (header_bar);

		var menu = new Menu ();
		menu.append ("Configuration", "app.configuration");
		menu.append ("About", "app.about");
		menu_button = new Gtk.MenuButton () {
			tooltip_text = "Menu",
			icon_name = "open-menu-symbolic",
			menu_model = menu,
			primary = true
		};
		header_bar.pack_end (menu_button);

		configuration_dialogue = new Adw.PreferencesDialog () {
			title = "Configuration",
			content_height = 400
		};
		configuration_dialogue.add (new ConfigurationPage (application_window));

		view_stack = new Adw.ViewStack () { enable_transitions = true };
		toolbar_view.content = view_stack;

		var release_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
		view_stack.add_named (release_box, "release");

		release_alert_revealer = new Gtk.Revealer () {
			transition_duration = 175,
			transition_type = Gtk.RevealerTransitionType.SLIDE_DOWN
		};
		release_box.append (release_alert_revealer);

		var release_alert_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
		release_alert_box.add_css_class ("error");
		release_alert_box.add_css_class ("alert");
		release_alert_revealer.child = release_alert_box;

		var release_alert_title_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
		release_alert_title_box.add_css_class ("alert-title");
		release_alert_box.append (release_alert_title_box);

		release_alert_title_box.append (new Gtk.Image.from_icon_name ("dialog-warning-symbolic") { icon_size = 12 });

		release_alert_title_box.append (new Gtk.Label ("Socket Error"));

		release_alert_title_box.append (new Gtk.Box (Gtk.Orientation.HORIZONTAL, 0) { hexpand = true });

		var release_alert_title_close = new Gtk.Button.from_icon_name ("window-close-symbolic") {
			halign = Gtk.Align.END,
			valign = Gtk.Align.CENTER,
			focus_on_click = false,
			tooltip_text = "Dismiss"
		};
		release_alert_title_close.add_css_class ("circular");
		release_alert_title_close.add_css_class ("flat");
		release_alert_title_close.clicked.connect (() => {
			release_alert_revealer.reveal_child = false;
		});
		release_alert_title_box.append (release_alert_title_close);

		release_alert_label = new Gtk.Label ("oifjioerjierjufjg") { halign = Gtk.Align.START };
		release_alert_box.append (release_alert_label);

		var release_reveal_button = new Gtk.Button ();
		release_reveal_button.clicked.connect (() => {
			release_alert_revealer.reveal_child = !release_alert_revealer.reveal_child;
		});
		release_box.append (release_reveal_button);

		var release_toolbar_view = new Adw.ToolbarView ();
		release_box.append (release_toolbar_view);

		var stable = new Release (application_window, ReleaseChannel.STABLE);
		var ptb = new Release (application_window, ReleaseChannel.PTB);
		var canary = new Release (application_window, ReleaseChannel.CANARY);

		var release_view_stack = new Adw.ViewStack () { enable_transitions = true };
		release_view_stack.add_titled_with_icon (stable, stable.channel.id, "Discord", "discord");
		release_view_stack.add_titled_with_icon (ptb, ptb.channel.id, "Discord PTB", "discord-ptb");
		release_view_stack.add_titled_with_icon (canary, canary.channel.id, "Discord Canary", "discord-canary");
		release_toolbar_view.content = release_view_stack;

		release_toolbar_view.add_top_bar (new Adw.ViewSwitcher () { stack = release_view_stack, policy = Adw.ViewSwitcherPolicy.WIDE });

		waiting_page = SuspensePageFactory.create ("Waiting…");
		view_stack.add_named (waiting_page, "waiting");

		critical_page = new Adw.StatusPage () {
			title = "Couldn't connect to backend",
			icon_name = "dialog-error"
		};
		view_stack.add_named (critical_page, "critical");

		var critical_page_reset_button = new Gtk.Button () {
			label = "Reset Connection",
			halign = Gtk.Align.CENTER,
			has_frame = true
		};
		critical_page_reset_button.set_size_request (100, 40);
		critical_page_reset_button.clicked.connect (() => Socket.start ());
		critical_page.child = critical_page_reset_button;

		add_action_entries ({
			{ "configuration", () => configuration_dialogue.present (application_window) },
			{ "about", () =>
			  Adw.show_about_dialog (
				                 application_window,
				                 "application-name",
				                 "Dislaunch",
				                 "developer-name",
				                 "Michael Fohqul"
			  ) }
		}, this);

		view_stack.visible_child_name = "waiting";
		refresh ({ {}, "Starting", null, null });
		application_window.present ();
		Socket.instance.state_sig.connect ((_, state) => refresh (state));
		Socket.start ();
		Socket.command ("state");
	}

	private void refresh (SocketState state) {
		if (state.critical != null) {
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
			critical_page.description = "Dislaunch encountered an error connecting to the backend: %s\n\n%s".printf (state.critical.message, comment);

			view_stack.visible_child_name = "critical";
		} else if (state.waiting != null) {
			waiting_page.title = state.waiting + "…";
			view_stack.visible_child_name = "waiting";
		} else {
			if (state.error != null) {
				release_alert_revealer.reveal_child = true;
				release_alert_label.label = state.error.message;
			} else {
				// release_alert_revealer.reveal_child = false;
				// release_alert_label.label = null;
			}
			view_stack.visible_child_name = "release";
		}
	}
}