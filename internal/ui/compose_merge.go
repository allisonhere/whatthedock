package ui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeServicesNode returns the mapping node under root's top-level
// "services" key so callers can read or mutate individual service blocks
// without disturbing anything else in the document — comments, key order,
// and unrelated top-level sections (networks, volumes, ...) all stay
// exactly as they were.
func composeServicesNode(root *yaml.Node) (*yaml.Node, error) {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, errors.New("empty compose document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("compose document is not a mapping")
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "services" {
			return doc.Content[i+1], nil
		}
	}
	return nil, errors.New("compose document has no services section")
}

// composeServiceNode finds service's key/value node pair under services,
// along with the key's index in services.Content (a key/value node pair
// occupies index/index+1) for callers that need to splice it out.
func composeServiceNode(services *yaml.Node, service string) (key, value *yaml.Node, index int, ok bool) {
	if services.Kind != yaml.MappingNode {
		return nil, nil, -1, false
	}
	for i := 0; i+1 < len(services.Content); i += 2 {
		if services.Content[i].Value == service {
			return services.Content[i], services.Content[i+1], i, true
		}
	}
	return nil, nil, -1, false
}

// baseComposeDefinesService reports whether content already defines service
// under top-level services — the signal that decides whether a create/edit
// merges directly into the base compose file (mergeComposeServiceFields)
// instead of going through a generated override, and whether Delete needs
// to remove the service's block from base (removeComposeService) rather
// than just cleaning up an override.
func baseComposeDefinesService(content []byte, service string) bool {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return false
	}
	services, err := composeServicesNode(&root)
	if err != nil {
		return false
	}
	_, _, _, ok := composeServiceNode(services, service)
	return ok
}

func setMappingScalar(node *yaml.Node, key, value string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			if value == "" {
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				return
			}
			node.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	if value == "" {
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func setMappingList(node *yaml.Node, key string, values []string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			if len(values) == 0 {
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				return
			}
			node.Content[i+1] = scalarSequence(values)
			return
		}
	}
	if len(values) == 0 {
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		scalarSequence(values),
	)
}

// setMappingEntries replaces node's `key` sequence (ports, volumes) with
// values, as setMappingList does, but without destroying what the form can't
// represent. The form loads only entries it can express as one short string,
// so rebuilding the list from it alone would silently delete the rest — a
// container-only `{target: 80}` port, an anonymous or tmpfs `{type: volume,
// target: /cache}` volume — the moment any other entry was edited.
//
// shortOf gives an existing entry's short form, or "" when the form can't
// represent it. Entries whose short form is still in values keep their
// original node (so long-form extras and quoting survive); new values get new
// nodes; entries the form can't represent are kept after them; entries the
// form could represent but no longer lists (the user removed them) are gone.
func setMappingEntries(node *yaml.Node, key string, values []string, shortOf func(*yaml.Node) string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		seq := node.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			setMappingList(node, key, values)
			return
		}
		type entry struct {
			node  *yaml.Node
			short string
			used  bool
		}
		existing := make([]entry, len(seq.Content))
		for j, item := range seq.Content {
			existing[j] = entry{node: item, short: shortOf(item)}
		}

		var next []*yaml.Node
		for _, value := range values {
			reused := false
			for j := range existing {
				if !existing[j].used && existing[j].short == value {
					existing[j].used = true
					next = append(next, existing[j].node)
					reused = true
					break
				}
			}
			if !reused {
				next = append(next, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
			}
		}
		for _, e := range existing {
			// A blank scalar is nothing to keep; anything else the form
			// couldn't read is.
			if e.short == "" && !(e.node.Kind == yaml.ScalarNode && strings.TrimSpace(e.node.Value) == "") {
				next = append(next, e.node)
			}
		}
		if len(next) == 0 {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
		seq.Content = next
		return
	}
	setMappingList(node, key, values)
}

// composePortShort is a ports entry's form representation: the string itself,
// or the short form of a long mapping that has both target and published.
func composePortShort(item *yaml.Node) string {
	switch item.Kind {
	case yaml.ScalarNode:
		return item.Value
	case yaml.MappingNode:
		var entry map[string]interface{}
		if item.Decode(&entry) == nil {
			return longComposePortToShort(entry)
		}
	}
	return ""
}

// composeVolumeShort is composePortShort's volumes counterpart.
func composeVolumeShort(item *yaml.Node) string {
	switch item.Kind {
	case yaml.ScalarNode:
		return item.Value
	case yaml.MappingNode:
		var entry map[string]interface{}
		if item.Decode(&entry) == nil {
			return longComposeVolumeToShort(entry)
		}
	}
	return ""
}

func scalarSequence(values []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	return seq
}

// composeFieldSet is a bitmask of the mergeable Compose service fields. It
// lets an apply touch only the fields the user actually changed, leaving the
// rest of the existing service block (including any field the form never
// loaded) untouched.
type composeFieldSet uint8

const (
	composeFieldImage composeFieldSet = 1 << iota
	composeFieldRestart
	composeFieldCommand
	composeFieldPorts
	composeFieldVolumes
	composeFieldEnvironment

	composeFieldAll = composeFieldImage | composeFieldRestart | composeFieldCommand |
		composeFieldPorts | composeFieldVolumes | composeFieldEnvironment
)

// mergeComposeServiceFields rewrites base so service's block matches the
// fields selected in changed — image, restart, command, ports, volumes, and
// environment — while leaving every other key already on the service
// (networks, depends_on, labels, build, ...), every other service, and the
// rest of the document (comments, key order) exactly as it was. Only the
// specific value lines being replaced can lose an inline comment of their
// own; nothing else is touched. Environment is always written back as a list
// of "KEY=value" strings regardless of whether it was previously a list or a
// map — both are valid Compose syntax, so this is a formatting choice, not a
// semantic one.
func mergeComposeServiceFields(base []byte, service string, fields composeOverrideService, changed composeFieldSet) ([]byte, error) {
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
	if changed&composeFieldImage != 0 {
		setMappingScalar(value, "image", fields.Image)
	}
	if changed&composeFieldRestart != 0 {
		setMappingScalar(value, "restart", fields.Restart)
	}
	if changed&composeFieldCommand != 0 {
		setMappingScalar(value, "command", normalizeComposeCommand(fields.Command))
	}
	if changed&composeFieldPorts != 0 {
		setMappingEntries(value, "ports", normalizeComposePorts(fields.Ports), composePortShort)
	}
	if changed&composeFieldVolumes != 0 {
		setMappingEntries(value, "volumes", normalizeComposeVolumes(fields.Volumes), composeVolumeShort)
	}
	if changed&composeFieldEnvironment != 0 {
		setMappingList(value, "environment", normalizeComposeEnvironment(fields.Environment))
	}
	return encodeComposeYAML(&root)
}

// removeComposeService deletes service's block from base entirely,
// preserving everything else — the base-file counterpart to Delete removing
// a WhatTheDock-generated override, for services the base file itself
// defines.
func removeComposeService(base []byte, service string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(base, &root); err != nil {
		return nil, err
	}
	services, err := composeServicesNode(&root)
	if err != nil {
		return nil, err
	}
	_, _, index, ok := composeServiceNode(services, service)
	if !ok {
		return nil, fmt.Errorf("service %q not found in base compose file", service)
	}
	services.Content = append(services.Content[:index], services.Content[index+2:]...)
	return encodeComposeYAML(&root)
}

func encodeComposeYAML(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// composeServiceFieldsFromContent parses content (a generated or
// hand-edited override's YAML — see createDraft.composeOverrideContent and
// applyOverrideFieldsFromYAML) and picks out the fields for service, the
// same way loading an existing override into the form already does.
func composeServiceFieldsFromContent(content, service string) (composeOverrideService, bool) {
	var doc composeOverrideDoc
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil || len(doc.Services) == 0 {
		return composeOverrideService{}, false
	}
	_, svc, ok := selectOverrideService(doc.Services, service)
	return svc, ok
}
