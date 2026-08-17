package ui

import (
	"bytes"
	"errors"
	"fmt"

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

func scalarSequence(values []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	return seq
}

// mergeComposeServiceFields rewrites base so service's block matches
// fields — image, restart, command, ports, volumes, and environment — while
// leaving every other key already on the service (networks, depends_on,
// labels, build, ...), every other service, and the rest of the document
// (comments, key order) exactly as it was. Only the specific value lines
// being replaced can lose an inline comment of their own; nothing else is
// touched. Environment is always written back as a list of "KEY=value"
// strings regardless of whether it was previously a list or a map — both
// are valid Compose syntax, so this is a formatting choice, not a semantic
// one.
func mergeComposeServiceFields(base []byte, service string, fields composeOverrideService) ([]byte, error) {
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
	setMappingScalar(value, "image", fields.Image)
	setMappingScalar(value, "restart", fields.Restart)
	setMappingScalar(value, "command", fields.Command)
	setMappingList(value, "ports", fields.Ports)
	setMappingList(value, "volumes", fields.Volumes)
	setMappingList(value, "environment", normalizeComposeEnvironment(fields.Environment))
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
