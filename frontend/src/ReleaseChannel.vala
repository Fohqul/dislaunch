class ReleaseChannel {
	private static ReleaseChannel _STABLE;
	public static ReleaseChannel STABLE {
		get {
			if (_STABLE == null)_STABLE = new ReleaseChannel("stable", "Discord");
			return _STABLE;
		}
	}

	private static ReleaseChannel _PTB;
	public static ReleaseChannel PTB {
		get {
			if (_PTB == null)_PTB = new ReleaseChannel("ptb", "Discord PTB");
			return _PTB;
		}
	}

	private static ReleaseChannel _CANARY;
	public static ReleaseChannel CANARY {
		get {
			if (_CANARY == null)_CANARY = new ReleaseChannel("canary", "Discord Canary");
			return _CANARY;
		}
	}

	public string id { get; private set; }
	public string title { get; private set; }

	private ReleaseChannel(string id, string title) {
		this.id = id;
		this.title = title;
	}

	public ReleaseState ? to_state(GlobalState global_state) {
		if (this == _STABLE)return global_state.stable;
		if (this == _PTB)return global_state.ptb;
		if (this == _CANARY)return global_state.canary;
		assert_not_reached();
	}
}
