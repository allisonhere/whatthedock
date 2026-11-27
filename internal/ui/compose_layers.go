package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/systems"
)

func composeServiceOverridePath(system config.System, base, service string) string {
	name := "compose.whatthedock." + safeComposeFilename(service) + ".yml"
	if system.Kind == "ssh" {
		return path.Join(path.Dir(base), name)
	}
	return filepath.Join(filepath.Dir(base), name)
}

func layeredServiceOverridePath(system config.System, base, service string, layers []string) string {
	file := composeServiceOverridePath(system, base, service)
	if len(layers) == 0 {
		return file
	}
	stem := strings.TrimSuffix(file, ".yml")
	for n := 2; slices.Contains(layers, file) && layers[len(layers)-1] != file; n++ {
		file = fmt.Sprintf("%s.%d.yml", stem, n)
	}
	return file
}

func (spec composeCreateSpec) files() []string {
	files := []string{spec.BaseFile}
	override := strings.TrimSuffix(spec.OverrideFile, ".tmp")
	added := false
	for _, file := range spec.AdditionalFiles {
		if override != "" && file == override {
			files = append(files, spec.OverrideFile)
			added = true
		} else {
			files = append(files, file)
		}
	}
	if spec.OverrideFile != "" && !added {
		files = append(files, spec.OverrideFile)
	}
	return files
}

func dockerCLIArgs(system config.System, args ...string) []string {
	host := systems.DockerHostFor(system)
	if system.Kind == "ssh" {
		host = ""
		if system.RemoteSocket != "" {
			host = "unix://" + system.RemoteSocket
		}
	}
	if host != "" {
		return append([]string{"--host", host}, args...)
	}
	return args
}

func dockerCLIEnv(cmd *exec.Cmd, system config.System) {
	if systems.DockerHostFor(system) != "" {
		cmd.Env = append(os.Environ(), "DOCKER_CONTEXT=")
	}
}

var dockerComposeOutput = runDockerComposeOutput

func runDockerComposeOutput(ctx context.Context, system config.System, project string, files []string, args ...string) ([]byte, error) {
	if system.Kind == "demo" {
		return nil, errors.New("Compose is unavailable in demo mode")
	}
	command := []string{"compose"}
	if project != "" {
		command = append(command, "-p", project)
	}
	for _, file := range files {
		command = append(command, "-f", file)
	}
	command = dockerCLIArgs(system, append(command, args...)...)
	if system.Kind == "ssh" {
		quoted := make([]string, len(command))
		for i, arg := range command {
			quoted[i] = systems.ShellQuote(arg)
		}
		executable := "docker "
		if system.RemoteSocket != "" {
			executable = "DOCKER_CONTEXT= docker "
		}
		return sshRun(ctx, system, executable+strings.Join(quoted, " "), "")
	}
	cmd := exec.CommandContext(ctx, "docker", command...)
	dockerCLIEnv(cmd, system)
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, errors.New(strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

type composeLayersMsg struct {
	epoch            uint64
	base, service    string
	merged, override string
	err              error
}

func (m Model) loadComposeLayersCmd(draft createDraft) tea.Cmd {
	system := m.activeSystemConfig()
	files := append([]string{draft.ComposeFile}, draft.composeLayers...)
	override := layeredServiceOverridePath(system, draft.ComposeFile, draft.Service, draft.composeLayers)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msg := composeLayersMsg{epoch: m.systemEpoch, base: draft.ComposeFile, service: draft.Service}
		data, err := dockerComposeOutput(ctx, system, draft.Project, files, "--profile", "*", "config", "--no-interpolate", "--format", "json")
		if err != nil {
			msg.err = err
			return msg
		}
		// Remote SSH returns stderr together with stdout. Compose JSON
		// starts and ends on their own lines even when it emits warnings.
		msg.merged = string(data)
		if start := strings.Index(msg.merged, "{\n"); start >= 0 {
			if end := strings.LastIndex(msg.merged, "\n}"); end > start {
				msg.merged = msg.merged[start : end+2]
			}
		}
		if slices.Contains(files, override) {
			raw, err := readComposeFile(ctx, system, override)
			msg.override, msg.err = raw, err
		}
		return msg
	}
}

func (m *Model) clearComposeLayers() {
	m.createDraft.composeLayers = nil
	m.createDraft.layersLoading = false
	m.createDraft.layersError = ""
}

func (m *Model) loadLayeredComposeFields(msg composeLayersMsg) error {
	draft := &m.createDraft
	if !baseComposeDefinesService([]byte(msg.merged), msg.service) {
		return fmt.Errorf("service %q is missing from the merged Compose configuration", msg.service)
	}
	edited := *draft
	changed := draft.changedComposeFields()
	draft.loadFields(msg.merged)
	if changed&composeFieldImage != 0 {
		draft.Image = edited.Image
	}
	if changed&composeFieldRestart != 0 {
		draft.Restart = edited.Restart
	}
	if changed&composeFieldCommand != 0 {
		draft.Command = edited.Command
	}
	if changed&composeFieldPorts != 0 {
		draft.Ports = edited.Ports
	}
	if changed&composeFieldVolumes != 0 {
		draft.Mounts = edited.Mounts
	}
	if changed&composeFieldEnvironment != 0 {
		draft.Env = edited.Env
	}
	if msg.override != "" {
		draft.OverrideRaw = msg.override
		draft.OverrideRawSet = true
		draft.OverrideLoaded = true
	}
	m.syncCreateFieldEditor()
	m.setCreateNotice("Loaded the merged Compose configuration", false)
	return nil
}

func layeredComposeChanges(draft createDraft, service string) (string, error) {
	raw := draft.OverrideRaw
	if !draft.OverrideRawSet {
		raw = fmt.Sprintf("services:\n  %q: {}\n", service)
	}
	fields, ok := composeServiceFieldsFromContent(draft.composeOverrideContent(), service)
	if !ok {
		return "", fmt.Errorf("could not read changes for service %q", service)
	}
	changed := draft.changedComposeFields()
	merged, err := mergeComposeServiceFields([]byte(raw), service, fields, changed)
	if err != nil {
		return "", err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(merged, &root); err != nil {
		return "", err
	}
	services, err := composeServicesNode(&root)
	if err != nil {
		return "", err
	}
	_, node, _, _ := composeServiceNode(services, service)
	for _, field := range []struct {
		key string
		bit composeFieldSet
	}{{"ports", composeFieldPorts}, {"volumes", composeFieldVolumes}, {"environment", composeFieldEnvironment}, {"command", composeFieldCommand}} {
		if changed&field.bit == 0 {
			continue
		}
		value := findMappingValue(node, field.key)
		if value == nil {
			// A cleared form field must explicitly remove its inherited value.
			value = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!reset"}
			if field.key == "command" {
				value = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!reset", Value: "null"}
			}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field.key}, value)
		} else if field.key != "command" {
			value.Tag = "!override"
		}
	}
	content, err := encodeComposeYAML(&root)
	return string(content), err
}

func findMappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func removeComposeLayer(ctx context.Context, system config.System, file string) error {
	if system.Kind == "ssh" {
		_, err := sshRun(ctx, system, "rm -f -- "+systems.ShellQuote(file), "")
		return err
	}
	if err := os.Remove(file); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func backupComposeLayer(ctx context.Context, system config.System, file string) error {
	if system.Kind == "ssh" {
		return backupComposeBaseRemote(ctx, system, file)
	}
	return backupComposeBase(file)
}

func stageComposeLayer(ctx context.Context, system config.System, file, stage string, content []byte) error {
	if system.Kind == "ssh" {
		return remoteWriteComposeTemp(ctx, system, stage, string(content), file)
	}
	return writeComposeTemp(stage, content, file)
}

func promoteComposeLayer(ctx context.Context, system config.System, stage, file string) error {
	if system.Kind == "ssh" {
		return remotePromoteComposeTemp(ctx, system, stage, file)
	}
	return promoteComposeTemp(stage, file)
}

func deleteLayeredComposeService(ctx context.Context, spec composeCreateSpec) error {
	spec = withExistingOverrideOnly(ctx, spec)
	files := spec.files()
	stagedFiles := slices.Clone(files)
	type layerChange struct {
		file, temp string
		before     []byte
	}
	var changed []layerChange
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, layer := range changed {
			_ = removeComposeLayer(cleanup, spec.System, layer.temp)
		}
	}()

	for i, file := range files {
		raw, err := readComposeFile(ctx, spec.System, file)
		data := []byte(raw)
		if err != nil {
			return fmt.Errorf("reading %s: %w", file, err)
		}
		if !baseComposeDefinesService(data, spec.Service) {
			continue
		}
		updated, err := removeComposeService(data, spec.Service)
		if err != nil {
			return err
		}
		temp := fmt.Sprintf("%s.whatthedock-%d.tmp", file, time.Now().UnixNano())
		changed = append(changed, layerChange{file: file, temp: temp, before: data})
		if err := stageComposeLayer(ctx, spec.System, file, temp, updated); err != nil {
			return err
		}
		stagedFiles[i] = temp
	}
	if len(changed) == 0 {
		return fmt.Errorf("service %q is defined outside the recorded Compose files", spec.Service)
	}
	output, err := dockerComposeOutput(ctx, spec.System, spec.Project, stagedFiles, "--profile", "*", "config", "--services")
	if err != nil {
		return fmt.Errorf("validating Compose files before deletion: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == spec.Service {
			return fmt.Errorf("service %q is still defined by an included Compose file", spec.Service)
		}
	}
	for _, layer := range changed {
		if err := backupComposeLayer(ctx, spec.System, layer.file); err != nil {
			return err
		}
	}
	if err := composeCommand(ctx, spec, "rm", "-sf", spec.Service); err != nil {
		return err
	}
	for i, layer := range changed {
		if err := promoteComposeLayer(ctx, spec.System, layer.temp, layer.file); err != nil {
			rollback, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			result := fmt.Errorf("updating %s after removing the container: %w", layer.file, err)
			for _, prior := range changed[:i+1] {
				if restoreErr := stageComposeLayer(rollback, spec.System, prior.file, prior.temp, prior.before); restoreErr != nil {
					result = errors.Join(result, restoreErr)
				} else if restoreErr := promoteComposeLayer(rollback, spec.System, prior.temp, prior.file); restoreErr != nil {
					result = errors.Join(result, restoreErr)
				}
			}
			return result
		}
	}
	return nil
}

func deleteLayeredComposeStack(ctx context.Context, spec composeCreateSpec) error {
	files := spec.files()
	for _, file := range files {
		if err := backupComposeLayer(ctx, spec.System, file); err != nil {
			return err
		}
	}
	if err := composeCommand(ctx, spec, "--profile", "*", "down"); err != nil {
		return err
	}
	for _, file := range files {
		if err := removeComposeLayer(ctx, spec.System, file); err != nil {
			return fmt.Errorf("removing %s after stopping the stack: %w", file, err)
		}
	}
	return nil
}
