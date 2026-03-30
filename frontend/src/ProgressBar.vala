class ProgressBar : Adw.Bin {
	private static string css = """
	progressbar trough, progressbar progress {
		min-height: 1.25em;
		border-radius: 4px;
	}

	progressbar trough progress {
		background-color: var(--purple-3);
	}
	""";

	public Gtk.ProgressBar progress_bar { get; private set; } // `progress_bar.progress_bar` is really unergonomic but ultimately unavoidable: https://discourse.gnome.org/t/gtk4-preference-of-composition-over-inheritence/3855

	private uint _progress;
	public uint progress {
		get {
			return _progress;
		}
		set {
			_progress = value;
			visible = true;
			Idle.add(() => {
				update_text();
				if (value < 101)
					progress_bar.fraction = value / 100.0;
				return Source.REMOVE;
			});
		}
	}

	private string _text = "";
	public string text {
		get {
			return _text;
		}
		set {
			_text = value;
			Idle.add(() => {
				update_text();
				return Source.REMOVE;
			});
		}
	}

	public ProgressBar() {
		Object();

		Css.add(css, Gtk.STYLE_PROVIDER_PRIORITY_USER);

		progress_bar = new Gtk.ProgressBar() { show_text = true };
		child = progress_bar;

		Timeout.add(200, () => {
			if (visible && progress > 100)
				progress_bar.pulse();

			return Source.CONTINUE;
		});
	}

	private void update_text() {
		if (progress < 101)
			progress_bar.text = text != "" ? "%s\n%u%%".printf(text, progress) : "%u%%".printf(progress);
		else
			progress_bar.text = text;
	}
}