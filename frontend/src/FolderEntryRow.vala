class FolderEntryRow : Adw.EntryRow {
	public delegate void PathHandler (string path);

	private Gtk.FileDialog file_dialog;

	public FolderEntryRow (Gtk.Window window, File initial_folder, PathHandler handler) {
		Object (
		        show_apply_button: true,
		        max_length: 4096 // this is `PATH_MAX` on Linux - must be a better way of getting this than hardcoding
		);

		apply.connect ((entry_row) => handler (entry_row.text));

		file_dialog = new Gtk.FileDialog () { initial_folder = initial_folder };

		var button = new Gtk.Button.from_icon_name ("document-open-folder");
		button.clicked.connect (() => file_dialog.select_folder.begin (window, null, (_, result) => {
			try {
				handler (file_dialog.select_folder.end (result).get_path ());
			} catch (Error e) {
				stderr.printf ("error selecting folder: %s\n", e.message);
			}
		}));
		add_suffix (button);
	}
}
