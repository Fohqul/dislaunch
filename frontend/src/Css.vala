class Css {
public static void add (string css, uint priority = Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION) {
	var css_provider = new Gtk.CssProvider ();
	css_provider.load_from_string (css);
	Gtk.StyleContext.add_provider_for_display (Gdk.Display.get_default (), css_provider, priority);
}
}