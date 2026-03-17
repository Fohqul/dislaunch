class Release : Gtk.Box {
	public ReleaseChannel channel { get; private set; }
	private Adw.ViewStack view_stack;
	private Adw.ActionRow update_row;
	private ProgressRow update_progress_row;
	private Gtk.Button update_button;
	private FolderEntryRow install_path_row;
	private ProgressRow install_path_progress_row;
	private Adw.EntryRow command_line_arguments_row;
	private ProgressRow uninstall_progress_row;
	private Gtk.StringList bd_channels;
	private Adw.ExpanderRow bd_enabled_row;
	private Gtk.Switch bd_enabled_switch;
	private Adw.ComboRow bd_channel_row;
	private ProgressRow bd_apply_progress_row;

	public Release (Adw.ApplicationWindow application_window, ReleaseChannel channel) {
		Object ();
		this.channel = channel;

		view_stack = new Adw.ViewStack ();
		append (view_stack);

		view_stack.add_named (new InstallPage (channel), "install");

		var preferences_page = new Adw.PreferencesPage ();
		view_stack.add_named (preferences_page, "preferences");

		var main_preferences_group = new Adw.PreferencesGroup ();
		preferences_page.add (main_preferences_group);

		update_row = new Adw.ActionRow () { title = "Loading…" };
		update_progress_row = new ProgressRow (update_row);
		main_preferences_group.add (update_progress_row);

		update_button = new Gtk.Button () { label = "Loading…", valign = Gtk.Align.CENTER };
		update_button.clicked.connect ((button) => Socket.command (channel.id + (button.label == "Update" ? " install" : " check_for_updates"))); // somewhat hacky to determine state using direct properties but oh well
		update_row.add_suffix (update_button);

		install_path_row = new FolderEntryRow (application_window, File.new_build_filename (Environment.get_user_state_dir (), "io.github.Fohqul.Dislaunch"), (path) => Socket.command ("%s move %s".printf (channel.id, path))) { title = "Install Path" };
		install_path_progress_row = new ProgressRow (install_path_row);
		main_preferences_group.add (install_path_progress_row);

		command_line_arguments_row = new Adw.EntryRow () { title = "Command-Line Arguments" };
		command_line_arguments_row.changed.connect ((row) => Socket.command ("%s command_line_arguments %s".printf (channel.id, row.text)));
		main_preferences_group.add (command_line_arguments_row);

		var uninstall_row = new Adw.ActionRow ();

		var uninstall_button = new Gtk.Button () {
			child = new Gtk.Label ("<b>Uninstall</b>") { use_markup = true },
			valign = Gtk.Align.CENTER
		};
		uninstall_button.add_css_class ("destructive-action");
		uninstall_button.clicked.connect (() => Socket.command (channel.id + " uninstall"));
		uninstall_row.add_suffix (uninstall_button);

		uninstall_progress_row = new ProgressRow (uninstall_row);
		main_preferences_group.add (uninstall_progress_row);

		var bd_preferences_group = new Adw.PreferencesGroup ();
		preferences_page.add (bd_preferences_group);

		bd_enabled_row = new Adw.ExpanderRow () { title = "BetterDiscord" };
		bd_preferences_group.add (bd_enabled_row);

		bd_enabled_switch = new Gtk.Switch () { valign = Gtk.Align.CENTER };
		bd_enabled_switch.state_set.connect ((_, state) => {
			Socket.command (channel.id + " bd_enabled " + (state ? "1" : "0"));
			return true;
		});
		bd_enabled_row.add_suffix (bd_enabled_switch);

		bd_channels = new Gtk.StringList ({ "Stable", "Canary" });
		bd_channel_row = new Adw.ComboRow () {
			title = "BetterDiscord Channel",
			model = bd_channels
		};
		bd_channel_row.notify["selected"].connect ((object, _) => {
			Adw.ComboRow? row = object as Adw.ComboRow;
			assert_nonnull (row);
			Gtk.StringObject? string_object = bd_channels.get_item (row.selected) as Gtk.StringObject;
			assert_nonnull (row);
			Socket.command (channel.id + " bd_channel " + string_object.string.ascii_down (string_object.string.length));
		});
		bd_enabled_row.add_row (bd_channel_row);

		var bd_apply_row = new Adw.ActionRow ();
		bd_preferences_group.add (bd_apply_row);

		var bd_apply_button = new Gtk.Button () { label = "Apply", valign = Gtk.Align.CENTER };
		bd_apply_button.add_css_class ("suggested-action");
		bd_apply_button.clicked.connect (() => {
			Gtk.StringObject? string_object = bd_channels.get_item (bd_channel_row.selected) as Gtk.StringObject;
			assert_nonnull (string_object);
			Socket.command ("%s bd_channel %s\n%s bd_enabled %b".printf (channel.id, string_object.string, channel.id, bd_enabled_switch.state));
		});
		bd_apply_row.add_suffix (bd_apply_button);

		bd_apply_progress_row = new ProgressRow (bd_apply_row);

		view_stack.visible_child_name = "preferences";
		// Socket.instance.state_sig.connect((_, state) => refresh(channel.to_state(state.backend_state)));
	}

	private void refresh (ReleaseState? state) {
		if (state == null || state.internal == null) {
			view_stack.visible_child_name = "install";
			return;
		}

		if (state.process != null) {
			update_progress_row.progress_bar.visible = false;
			install_path_progress_row.progress_bar.visible = false;
			uninstall_progress_row.progress_bar.visible = false;
			bd_apply_progress_row.progress_bar.visible = false;

			switch (state.process.status) {
			case "" :
				break;
			case "download" :
			case "install" :
			case "update_check" :
				update_progress_row.progress_bar.progress = state.process.progress;
				break;
			case "bd_injection":
				bd_apply_progress_row.progress_bar.progress = state.process.progress;
				break;
			case "move":
				install_path_progress_row.progress_bar.progress = state.process.progress;
				break;
			case "uninstall":
				uninstall_progress_row.progress_bar.progress = state.process.progress;
				break;
			case "fatal":
				view_stack.visible_child_name = "recover";
				return;
			default:
				stderr.printf ("Unrecognised status: %s\n", state.process.status);
				break;
			}
		}

		if (state.version != state.internal.latest_version) {
			update_row.title = "Installed version: %s (update available to %s)".printf (state.version, state.internal.latest_version);
			update_button.label = "Update";
		} else {
			update_row.title = "Installed version: " + state.version;
			update_button.label = "Check for updates";
		}
		update_row.subtitle = state.internal.last_checked.to_unix () != 0 ? state.internal.last_checked.format ("Last checked: %Y-%m-%d %H:%M:%S") : "";
		install_path_row.text = state.internal.install_path;
		command_line_arguments_row.text = state.internal.command_line_arguments;

		bd_enabled_row.expanded = state.internal.bd_enabled;
		bd_enabled_switch.state = state.internal.bd_enabled;
		bd_enabled_switch.active = state.internal.bd_enabled;
		switch (state.internal.bd_channel) {
		case "stable":
			bd_channel_row.selected = 0;
			break;
		case "canary":
			bd_channel_row.selected = 1;
			break;
		default:
			assert_not_reached ();
		}
		view_stack.visible_child_name = "preferences";
	}
}