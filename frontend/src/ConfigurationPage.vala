class ConfigurationPage : Adw.PreferencesPage {
	private Adw.ExpanderRow automatically_check_for_updates_row;
	private Gtk.Switch automatically_check_for_updates_switch;
	private Gtk.Switch notify_on_update_available_switch;
	private Gtk.Switch automatically_install_updates_switch;
	private FolderEntryRow default_install_path_row;

	public ConfigurationPage (Adw.ApplicationWindow application_window) {
		Object ();

		var preferences_group = new Adw.PreferencesGroup ();
		add (preferences_group);

		automatically_check_for_updates_row = new Adw.ExpanderRow () { title = "Automatically check for updates" };
		preferences_group.add (automatically_check_for_updates_row);

		automatically_check_for_updates_switch = new Gtk.Switch () { valign = Gtk.Align.CENTER };
		automatically_check_for_updates_switch.state_set.connect ((_, state) => {
			Socket.command ("config automatically_check_for_updates " + (state ? "1" : "0"));
			return true;
		});
		automatically_check_for_updates_row.add_suffix (automatically_check_for_updates_switch);

		var notify_on_update_available_row = new Adw.ActionRow () { title = "Notify me when an update is available" };
		automatically_check_for_updates_row.add_row (notify_on_update_available_row);

		notify_on_update_available_switch = new Gtk.Switch () { valign = Gtk.Align.CENTER };
		notify_on_update_available_switch.state_set.connect ((_, state) => {
			Socket.command ("config notify_on_update_available " + (state ? "1" : "0"));
			return true;
		});
		notify_on_update_available_row.add_suffix (notify_on_update_available_switch);

		var automatically_install_updates_row = new Adw.ActionRow () { title = "Automatically install available updates" };
		automatically_check_for_updates_row.add_row (automatically_install_updates_row);

		automatically_install_updates_switch = new Gtk.Switch () { valign = Gtk.Align.CENTER };
		automatically_install_updates_switch.state_set.connect ((_, state) => {
			Socket.command ("config automatically_install_updates " + (state ? "1" : "0"));
			return true;
		});
		automatically_install_updates_row.add_suffix (automatically_install_updates_switch);

		default_install_path_row = new FolderEntryRow (application_window, File.new_build_filename (Environment.get_user_data_dir (), "io.github.Fohqul.Dislaunch"), (path) => Socket.command ("config default_install_path " + path)) {
			title = "Default install path",
			tooltip_text = "Only applies to new installations. For an already installed release, change its path from the main dashboard."
		};
		preferences_group.add (default_install_path_row);

		Socket.instance.state_sig.connect ((_, state) => {
			Configuration config = state.global_state.config;
			automatically_check_for_updates_row.expanded = config.automatically_check_for_updates;
			automatically_check_for_updates_switch.state = config.automatically_check_for_updates;
			automatically_check_for_updates_switch.active = config.automatically_check_for_updates;
			notify_on_update_available_switch.state = config.notify_on_update_available;
			notify_on_update_available_switch.active = config.notify_on_update_available;
			automatically_install_updates_switch.state = config.automatically_install_updates;
			automatically_install_updates_switch.active = config.automatically_install_updates;
			default_install_path_row.text = config.default_install_path == null ? "" : config.default_install_path;
		});
	}
}
