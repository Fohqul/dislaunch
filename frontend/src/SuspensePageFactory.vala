class SuspensePageFactory {
	public static Adw.StatusPage create(string title) {
		var status_page = new Adw.StatusPage() {
			title = title
		};

		var spinner_paintable = new Adw.SpinnerPaintable(status_page);
		status_page.paintable = spinner_paintable;

		return status_page;
	}
}