package service

// systemd unit name.
const Name = "cortendesk-agent"

var dependencies = []string{"After=network-online.target", "Wants=network-online.target"}
