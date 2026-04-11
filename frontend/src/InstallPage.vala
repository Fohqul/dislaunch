class InstallPage : Adw.Bin {
	private ReleaseChannel channel;
	private Adw.StatusPage status_page;
	private Gtk.Button install_button;
	private Adw.SpinnerPaintable spinner_paintable;
	private ProgressBar progress_bar;

	public InstallPage(ReleaseChannel channel) {
		this.channel = channel;

		status_page = new Adw.StatusPage() {
			halign = Gtk.Align.FILL,
			hexpand = true,
			vexpand = true
		};
		child = status_page;

		install_button = new Gtk.Button() {
			label = "Install " + channel.title,
			halign = Gtk.Align.CENTER,
			has_frame = true
		};
		install_button.set_size_request(100, 40);
		install_button.clicked.connect(() => channel.command("install"));

		spinner_paintable = new Adw.SpinnerPaintable(status_page);

		progress_bar = new ProgressBar();

		Socket.on_state((state) => refresh(channel.to_state(state.backend_state)));
	}

	private void refresh(ReleaseState? state) {
		if (state == null || state.status == "") {
			status_page.title = channel.title + " is not installed";
			status_page.description = "";
			status_page.icon_name = "system-software-install-symbolic";
			status_page.child = install_button;
			return;
		}

		status_page.title = "Installing " + channel.title;
		status_page.description = state.error != "" ? "%s\n\n%s".printf(state.message, state.error) : state.message;
		status_page.paintable = spinner_paintable;
		status_page.child = progress_bar;
		progress_bar.progress = state.progress;
	}
}