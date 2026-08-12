package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const settingsFileName = "settings.json"

type Settings struct {
	GraphStyle      string `json:"graphStyle,omitempty"`
	GraphColor      string `json:"graphColor,omitempty"`
	LogColor        string `json:"logColor,omitempty"`
	ShowDeltas      *bool  `json:"showDeltas,omitempty"`
	StatsRefresh    string `json:"statsRefresh,omitempty"`
	DefaultActivity string `json:"defaultActivity,omitempty"`
}

func SettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tidedock", settingsFileName), nil
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
