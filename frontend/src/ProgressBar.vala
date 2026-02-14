class ProgressBar : Gtk.Widget {
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
			if (value < 101)
				progress_bar.fraction = value / 100.0;
		}
	}

	public ProgressBar () {
		Object ();

		Css.add (css, Gtk.STYLE_PROVIDER_PRIORITY_USER);

		progress_bar = new Gtk.ProgressBar ();
		progress_bar.set_parent (this);

		Timeout.add (200, () => {
			if (!visible)
				return Source.CONTINUE;

			if (progress > 100)
				progress_bar.pulse ();

			return Source.CONTINUE;
		});
	}

	static construct {
		set_layout_manager_type (typeof (Gtk.BinLayout));
	}
}
