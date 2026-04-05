class SuspensePageFactory {
	public static Adw.StatusPage create(string title) {
		var status_page = new Adw.StatusPage() {
			title = title
		};

		status_page.paintable = new Adw.SpinnerPaintable(status_page);

		return status_page;
	}
}