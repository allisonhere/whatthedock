package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/allisonhere/whatthedock/internal/systems"
)

// A service defined in the base compose file AND in a WhatTheDock override
// has an effective definition that is the two layered. Editing it loads the
// form from the override, so when the edit is applied into the base file the
// override has to be folded in rather than just deleted — otherwise every
// field only the override set (and every key the form doesn't manage at all)
// silently reverts to the base's.

// composeFormKeys are the service keys the form manages; everything else on a
// service (labels, networks, healthcheck, ...) is not the form's to rewrite.
var composeFormKeys = map[string]composeFieldSet{
	"image":       composeFieldImage,
	"restart":     composeFieldRestart,
	"command":     composeFieldCommand,
	"ports":       composeFieldPorts,
	"volumes":     composeFieldVolumes,
	"environment": composeFieldEnvironment,
}

// overrideServiceNode finds service's mapping under the document's top-level
// services, falling back to the only service there is — the same selection
// composeServiceFieldsFromContent makes.
func overrideServiceNode(root *yaml.Node, service string) (services *yaml.Node, index int, value *yaml.Node, ok bool) {
	services, err := composeServicesNode(root)
	if err != nil {
		return nil, -1, nil, false
	}
	if _, value, index, ok = composeServiceNode(services, strings.TrimSpace(service)); ok {
		return services, index, value, true
	}
	if len(services.Content) == 2 {
		return services, 0, services.Content[1], true
	}
	return nil, -1, nil, false
}

// composeFieldsPresent is which of the form-managed keys content's service
// actually sets (as opposed to leaving to the base).
func composeFieldsPresent(content, service string) composeFieldSet {
	var root yaml.Node
	if yaml.Unmarshal([]byte(content), &root) != nil {
		return 0
	}
	_, _, value, ok := overrideServiceNode(&root, service)
	if !ok || value.Kind != yaml.MappingNode {
		return 0
	}
	var present composeFieldSet
	for i := 0; i+1 < len(value.Content); i += 2 {
		present |= composeFormKeys[value.Content[i].Value]
	}
	return present
}

// foldOverrideIntoBase writes overrideContent's form-managed fields into
// base's service block the way Compose would have layered them: scalars
// (image, restart, command) replace the base's; ports and volumes are added
// to the base's (a volume at the same container path replaces it);
// environment is merged by variable name, the override winning. Fields the
// override doesn't set are left exactly as the base has them.
func foldOverrideIntoBase(base []byte, service, overrideContent string) ([]byte, error) {
	override, ok := composeServiceFieldsFromContent(overrideContent, service)
	if !ok {
		return nil, fmt.Errorf("could not read fields for service %q", service)
	}
	present := composeFieldsPresent(overrideContent, service)

	var root yaml.Node
	if err := yaml.Unmarshal(base, &root); err != nil {
		return nil, err
	}
	services, err := composeServicesNode(&root)
	if err != nil {
		return nil, err
	}
	_, value, _, ok := composeServiceNode(services, service)
	if !ok {
		return nil, fmt.Errorf("service %q not found in base compose file", service)
	}
	var existing composeOverrideService
	_ = value.Decode(&existing) // keys it doesn't model are ignored; a bad one just leaves existing empty

	if present&composeFieldImage != 0 {
		setMappingScalar(value, "image", override.Image)
	}
	if present&composeFieldRestart != 0 {
		setMappingScalar(value, "restart", override.Restart)
	}
	if present&composeFieldCommand != 0 {
		setMappingScalar(value, "command", normalizeComposeCommand(override.Command))
	}
	if present&composeFieldPorts != 0 {
		merged := layerEntries(normalizeComposePorts(existing.Ports), normalizeComposePorts(override.Ports), func(s string) string { return s })
		setMappingEntries(value, "ports", merged, composePortShort)
	}
	if present&composeFieldVolumes != 0 {
		merged := layerEntries(normalizeComposeVolumes(existing.Volumes), normalizeComposeVolumes(override.Volumes), mountTarget)
		setMappingEntries(value, "volumes", merged, composeVolumeShort)
	}
	if present&composeFieldEnvironment != 0 {
		merged := layerEntries(normalizeComposeEnvironment(existing.Environment), normalizeComposeEnvironment(override.Environment), envName)
		setMappingList(value, "environment", merged)
	}
	return encodeComposeYAML(&root)
}

// layerEntries is base with over layered on top: a base entry that shares a
// key with an override entry is replaced by it, the rest of base stays in
// place, and the override's entries follow.
func layerEntries(base, over []string, key func(string) string) []string {
	overKeys := make(map[string]bool, len(over))
	for _, entry := range over {
		overKeys[key(entry)] = true
	}
	out := make([]string, 0, len(base)+len(over))
	for _, entry := range base {
		if !overKeys[key(entry)] {
			out = append(out, entry)
		}
	}
	return append(out, over...)
}

// mountTarget is a "source:target[:mode]" volume's container path, the key
// Compose merges volumes on.
func mountTarget(entry string) string {
	parts := strings.Split(entry, ":")
	if len(parts) >= 2 {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(entry)
}

// envName is a "NAME=value" entry's variable name.
func envName(entry string) string {
	name, _, _ := strings.Cut(entry, "=")
	return strings.TrimSpace(name)
}

// residualOverride is override content with service's form-managed keys
// removed (they now live in the base), and whether anything is left: keys the
// form doesn't manage, other services, other top-level sections. Those have
// no home in the base merge, so deleting the file would lose them.
func residualOverride(content []byte, service string) ([]byte, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, false, err
	}
	services, index, value, ok := overrideServiceNode(&root, service)
	if !ok || value.Kind != yaml.MappingNode {
		return content, true, nil // not a shape we understand: leave it alone
	}
	kept := value.Content[:0:0]
	for i := 0; i+1 < len(value.Content); i += 2 {
		if _, managed := composeFormKeys[value.Content[i].Value]; !managed {
			kept = append(kept, value.Content[i], value.Content[i+1])
		}
	}
	value.Content = kept
	if len(kept) == 0 {
		services.Content = append(services.Content[:index], services.Content[index+2:]...)
	}
	if len(services.Content) == 0 {
		// Drop the empty services section; anything else at the top level
		// (networks, volumes, ...) still counts as left over.
		doc := root.Content[0]
		for i := 0; i+1 < len(doc.Content); i += 2 {
			if doc.Content[i].Value == "services" {
				doc.Content = append(doc.Content[:i], doc.Content[i+2:]...)
				break
			}
		}
		if len(doc.Content) == 0 {
			return nil, false, nil
		}
	}
	out, err := encodeComposeYAML(&root)
	return out, err == nil, err
}

// mergeBaseFields is the base-file side of applying a draft: the override's
// fields folded in when the draft was loaded from one, else just the fields
// the user changed.
func mergeBaseFields(baseContent []byte, spec composeCreateSpec) ([]byte, error) {
	if spec.FoldOverride {
		return foldOverrideIntoBase(baseContent, spec.Service, spec.Content)
	}
	fields, ok := composeServiceFieldsFromContent(spec.Content, spec.Service)
	if !ok {
		return nil, fmt.Errorf("could not read fields for service %q", spec.Service)
	}
	return mergeComposeServiceFields(baseContent, spec.Service, fields, spec.ChangedFields)
}

// retireOverrideLocal is what happens to the override file once its fields are
// in the base: removed, unless it holds keys the base merge can't carry, in
// which case it is rewritten without the fields that moved. The result says
// whether an override remains, so the follow-up compose commands still layer
// it. A draft that wasn't loaded from the override just removes it, as before.
func retireOverrideLocal(spec composeCreateSpec) (bool, error) {
	path := strings.TrimSpace(spec.OverrideFile)
	if path == "" {
		return false, nil
	}
	if !spec.FoldOverride {
		_ = os.Remove(path)
		return false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rest, keep, err := residualOverride(data, spec.Service)
	if err != nil || !keep {
		if err == nil {
			_ = os.Remove(path)
		}
		return false, err
	}
	temp := path + ".tmp"
	if err := writeComposeTemp(temp, rest, path); err != nil {
		return false, err
	}
	if err := promoteComposeTemp(temp, path); err != nil {
		_ = os.Remove(temp)
		return false, err
	}
	return true, nil
}

// retireOverrideRemote is retireOverrideLocal over SSH.
func retireOverrideRemote(ctx context.Context, spec composeCreateSpec) (bool, error) {
	path := strings.TrimSpace(spec.OverrideFile)
	if path == "" {
		return false, nil
	}
	remove := func() { _, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(path), "") }
	if !spec.FoldOverride {
		remove()
		return false, nil
	}
	data, err := sshRun(ctx, spec.System, "cat "+systems.ShellQuote(path), "")
	if err != nil {
		return false, nil // no override to retire
	}
	rest, keep, err := residualOverride(data, spec.Service)
	if err != nil {
		return false, err
	}
	if !keep {
		remove()
		return false, nil
	}
	temp := path + ".tmp"
	if err := remoteWriteComposeTemp(ctx, spec.System, temp, string(rest), path); err != nil {
		return false, err
	}
	if err := remotePromoteComposeTemp(ctx, spec.System, temp, path); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(temp), "")
		return false, err
	}
	return true, nil
}
