class SuspensePageFactory {
	public static Adw.StatusPage create(string title) {
		Adw.StatusPage status_page = new Adw.StatusPage() {
			title = title
		};

		// Note that something about my Ubuntu install is completely broken
		// such that `Adw.Spinner`s used outside of Flatpak apps
		// simply do not spin so I cannot confirm this works correctly rn
		// but it should be fine
		Adw.SpinnerPaintable spinner_paintable = new Adw.SpinnerPaintable(status_page);
		status_page.paintable = spinner_paintable;

		return status_page;
	}
}