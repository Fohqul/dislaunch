class Application : Adw.Application {
private static string css =
	"""
	.release-alert {
		background-color: alpha(var(--error-color), 0.3);
		margin: 0.8em;
		border: 1px solid var(--error-bg-color);
		border-radius: 8px;
	}

	.release-alert-title {
		background-color: var(--error-bg-color);
		color: var(--error-fg-color);
		font-weight: bold;
		border: 2px solid var(--error-bg-color);
		padding-left: 0.6em;
		border-radius: 6px 6px 0 0;
	}

	.release-alert-title label {
		line-height: 1.0;
	}

	.release-alert-title button > image {
		color: var(--error-fg-color);
	}

	.release-alert > label {
		margin: 0.6em;
	}
	""";
private ReleaseChannel[] channels = { ReleaseChannel.STABLE, ReleaseChannel.PTB, ReleaseChannel.CANARY };

private Adw.ApplicationWindow application_window;
private Adw.HeaderBar header_bar;
private Gtk.MenuButton menu_button;
private Adw.PreferencesDialog configuration_dialogue;
private Adw.ViewStack view_stack;
private Gtk.Revealer release_alert_revealer;
private Gtk.Label release_alert_label;
private Adw.ViewStackPage[] release_pages;
private SimpleAction configuration_action;

public Application () {
	Object (application_id: "io.github.Fohqul.Dislaunch", flags: ApplicationFlags.DEFAULT_FLAGS);
}

public override void activate () {
	Css.add (css);

	application_window = new Adw.ApplicationWindow (this) {
		title = "Dislaunch",
		default_height = 600,
		default_width = 550,
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

	view_stack = new Adw.ViewStack () {
		enable_transitions = true
	};
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
	release_alert_box.add_css_class ("release-alert");
	release_alert_revealer.child = release_alert_box;

	var release_alert_title_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
	release_alert_title_box.add_css_class ("release-alert-title");
	release_alert_box.append (release_alert_title_box);

	release_alert_title_box.append (
		new Gtk.Image.from_icon_name ("dialog-warning-symbolic") {
			icon_size = 12
		}
	);

	release_alert_title_box.append (new Gtk.Label ("Socket Error"));

	release_alert_title_box.append (
		new Gtk.Box (Gtk.Orientation.HORIZONTAL, 0) {
			hexpand = true
		}
	);

	var release_alert_title_close = new Gtk.Button.from_icon_name ("window-close-symbolic") {
		halign = Gtk.Align.END,
		valign = Gtk.Align.CENTER,
		focus_on_click = false,
		tooltip_text = "Dismiss"
	};
	release_alert_title_close.add_css_class ("circular");
	release_alert_title_close.add_css_class ("flat");
	release_alert_title_close.clicked.connect (
		() => {
			release_alert_revealer.reveal_child = false;
		}
	);
	release_alert_title_box.append (release_alert_title_close);

	release_alert_label = new Gtk.Label ("oifjioerjierjufjg") {
		halign = Gtk.Align.START
	};
	release_alert_box.append (release_alert_label);

	var release_reveal_button = new Gtk.Button ();
	release_reveal_button.clicked.connect (
		() => {
			release_alert_revealer.reveal_child = !release_alert_revealer.reveal_child;
		}
	);
	// release_box.append (release_reveal_button);

	var release_toolbar_view = new Adw.ToolbarView ();
	release_box.append (release_toolbar_view);

	var release_view_stack = new Adw.ViewStack () {
		enable_transitions = true
	};
	release_pages = new Adw.ViewStackPage[3];
	for (uint8 i = 0; i < channels.length; ++i)
		release_pages[i] = release_view_stack.add_titled (
			new Release (application_window, channels[i]),
			channels[i].id, channels[i].title
		);
	release_toolbar_view.content = release_view_stack;

	release_toolbar_view.add_top_bar (
		new Adw.InlineViewSwitcher () {
			can_shrink = false, homogeneous = true, stack = release_view_stack
		}
	);

	view_stack.add_named (new SocketPage (), "socket");

	configuration_action = new SimpleAction ("configuration", null);
	configuration_action.activate.connect (() => configuration_dialogue.present (application_window));

	add_action_entries (
		{
			{ "about", () =>
			  Adw.show_about_dialog (
				  application_window,
				  "application-name",
				  "Dislaunch",
				  "developer-name",
				  "Michael Fohqul",
				  "comments",
				  "<b>This is beta software</b> and may contain bugs and/or security vulnerabilities. The developer accepts no responsibility or liability for potential losses incurred by its use.",
				  "website",
				  "https://github.com/Fohqul/dislaunch",
				  "issue-url",
				  "https://github.com/Fohqul/dislaunch/issues",
				  "copyright",
				  "© 2026 Fohqul",
				  "license-type",
				  Gtk.License.GPL_3_0
			  ) }
		}, this
	);

	view_stack.visible_child_name = "socket";
	refresh ({ {}, "Starting", null, null });
	application_window.present ();
	Socket.on_state (refresh);
	Socket.start ();
	Socket.command ("state");
}

private void refresh (SocketState state) {
	if (state.critical != null || state.waiting != null) {
		if (configuration_dialogue.parent != null)
			configuration_dialogue.close ();
		remove_action ("configuration");
		view_stack.visible_child_name = "socket";
		return;
	}

	add_action (configuration_action);

	if (state.error != null) {
		release_alert_revealer.reveal_child = true;
		release_alert_label.label = state.error.message;
	} else {
		// release_alert_revealer.reveal_child = false;
		// release_alert_label.label = null;
	}

	for (uint8 i = 0; i < channels.length; ++i) {
		var release_state = channels[i].to_state (state.backend_state);
		var installed = release_state.version != "" && release_state.internal != null;
		release_pages[i].needs_attention = installed && release_state.internal.latest_version != "" &&
			release_state.version != release_state.internal.latest_version;
	}

	view_stack.visible_child_name = "release";
}
}