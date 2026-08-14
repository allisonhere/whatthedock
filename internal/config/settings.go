package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const settingsFileName = "settings.json"

type Settings struct {
	GraphStyle      string   `json:"graphStyle,omitempty"`
	GraphColor      string   `json:"graphColor,omitempty"`
	LogColor        string   `json:"logColor,omitempty"`
	LogHealthColor  *bool    `json:"logHealthColor,omitempty"`
	ShowDeltas      *bool    `json:"showDeltas,omitempty"`
	CreateVim       *bool    `json:"createVim,omitempty"`
	StatsRefresh    string   `json:"statsRefresh,omitempty"`
	DefaultActivity string   `json:"defaultActivity,omitempty"`
	ActiveSystem    string   `json:"activeSystem,omitempty"`
	Systems         []System `json:"systems,omitempty"`
}

type System struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	DockerHost   string `json:"dockerHost,omitempty"`
	SSHHost      string `json:"sshHost,omitempty"`
	SSHUser      string `json:"sshUser,omitempty"`
	SSHPort      string `json:"sshPort,omitempty"`
	SSHAuth      string `json:"sshAuth,omitempty"`
	RemoteSocket string `json:"remoteSocket,omitempty"`
	LocalSocket  string `json:"localSocket,omitempty"`
}

func SettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "whatthedock", settingsFileName), nil
}

func LoadSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func SaveSettings(path string, settings Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func DefaultSystem() System {
	return System{ID: "local", Name: "local", Kind: "local"}
}

func NormalizeSystems(settings Settings) Settings {
	if len(settings.Systems) == 0 {
		settings.Systems = []System{DefaultSystem()}
	}
	for i := range settings.Systems {
		if settings.Systems[i].ID == "" {
			settings.Systems[i].ID = systemID(settings.Systems[i].Name)
		}
		if settings.Systems[i].Name == "" {
			settings.Systems[i].Name = settings.Systems[i].ID
		}
		if settings.Systems[i].Kind == "" {
			settings.Systems[i].Kind = "local"
		}
		if settings.Systems[i].Kind == "ssh" {
			if user, host, ok := splitSSHUserHost(settings.Systems[i].SSHHost); ok {
				if settings.Systems[i].SSHUser == "" {
					settings.Systems[i].SSHUser = user
				}
				settings.Systems[i].SSHHost = host
			}
			switch settings.Systems[i].SSHAuth {
			case "password":
			default:
				settings.Systems[i].SSHAuth = "config"
			}
			if settings.Systems[i].RemoteSocket == "" {
				settings.Systems[i].RemoteSocket = "/var/run/docker.sock"
			}
			if settings.Systems[i].LocalSocket == "" {
				settings.Systems[i].LocalSocket = filepath.Join(os.TempDir(), "whatthedock-"+settings.Systems[i].ID+".sock")
			}
		} else {
			settings.Systems[i].SSHUser = ""
			settings.Systems[i].SSHPort = ""
			settings.Systems[i].SSHAuth = ""
		}
	}
	if settings.ActiveSystem == "" || FindSystem(settings.Systems, settings.ActiveSystem) == nil {
		settings.ActiveSystem = settings.Systems[0].ID
	}
	return settings
}

func splitSSHUserHost(value string) (string, string, bool) {
	at := -1
	for i, r := range value {
		if r == '@' {
			at = i
			break
		}
	}
	if at <= 0 || at >= len(value)-1 {
		return "", "", false
	}
	return value[:at], value[at+1:], true
}

func FindSystem(systems []System, id string) *System {
	for i := range systems {
		if systems[i].ID == id {
			return &systems[i]
		}
	}
	return nil
}

func systemID(name string) string {
	id := ""
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			id += string(r)
		case r >= 'A' && r <= 'Z':
			id += string(r + ('a' - 'A'))
		case id != "" && id[len(id)-1] != '-':
			id += "-"
		}
	}
	if id == "" {
		return "system"
	}
	return id
}
