class ProgressRow : Adw.PreferencesRow {
	public ProgressBar progress_bar { get; private set; }

	public ProgressRow (Adw.PreferencesRow preferences_row) {
		Object ();

		var box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
		child = box;
		preferences_row.unparent (); // HACK: `preferences_row` is parented for some reason, which makes `box.append` fail
		box.append (preferences_row);

		progress_bar = new ProgressBar ();
		progress_bar.progress_bar.visible = false;
		box.append (progress_bar);
	}
}