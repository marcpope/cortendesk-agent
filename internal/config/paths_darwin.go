package config

func Default() Paths {
	dir := "/Library/Application Support/CortenDesk Agent"
	return Paths{
		Binary:    "/Library/CortenDesk Agent/cortendesk-agent",
		ConfigDir: dir,
		Config:    dir + "/config.json",
		State:     dir + "/state.json",
		CA:        dir + "/ca.pem",
		Commands:  dir + "/commands.json",
		Downloads: dir + "/downloads",
		Log:       "/Library/Logs/CortenDesk Agent/agent.log",
	}
}
