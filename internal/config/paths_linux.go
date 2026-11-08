package config

func Default() Paths {
	dir := "/etc/cortendesk-agent"
	return Paths{
		Binary:    "/usr/local/bin/cortendesk-agent",
		ConfigDir: dir,
		Config:    dir + "/config.json",
		State:     dir + "/state.json",
		CA:        dir + "/ca.pem",
		Commands:  dir + "/commands.json",
		Downloads: dir + "/downloads",
	}
}
