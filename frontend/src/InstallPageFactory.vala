// In any other context, I would just write a normal-ass function
// that returns an instance of `Adw.StatusPage`. Unfortunately,
// OOP best practices mandate that that function is a static
// member of a so-called "factory", which you will never convince
// me is not just a namespace for a function that returns an object
//
// I also can't just write a normal function because `Release.Channel`
// isn't accessible in that scope for some reason
class InstallPageFactory {
	public static Adw.StatusPage create(ReleaseChannel channel) {
		var status_page = new Adw.StatusPage() {
			title = channel.title + " is not installed",
			icon_name = "system-software-install-symbolic",
			halign = Gtk.Align.FILL,
			hexpand = true,
			vexpand = true
		};

		var button = new Gtk.Button() {
			label = "Install " + channel.title,
			halign = Gtk.Align.CENTER,
			has_frame = true
		};
		button.set_size_request(100, 40);
		button.clicked.connect(() => Socket.command(channel.id + " install"));
		status_page.child = button;

		return status_page;
	}
}
