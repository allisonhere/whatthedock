// Package composefile holds small, dependency-light helpers for inspecting a
// Compose document at the YAML-node level. It exists mainly so the doctor
// command can compare a live compose file against WhatTheDock's own pre-apply
// backups without importing the whole TUI package.
package composefile

import (
	"sort"

	"gopkg.in/yaml.v3"
)

// ManagedKeys are the service keys the WhatTheDock form owns and is allowed
// to change or remove (image/restart/command/ports/volumes/environment). A
// loss outside this set is never a deliberate form edit.
var ManagedKeys = map[string]bool{
	"image":       true,
	"restart":     true,
	"command":     true,
	"ports":       true,
	"volumes":     true,
	"environment": true,
}

// ServiceKeys returns the set of top-level mapping keys on service's block in
// a Compose document. A document that doesn't define the service yields an
// empty set, not an error, so callers can compare across versions where the
// service appeared or disappeared.
func ServiceKeys(content []byte, service string) (map[string]bool, error) {
	keys := map[string]bool{}
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, err
	}
	svc, ok := serviceNode(&root, service)
	if !ok {
		return keys, nil
	}
	return keysFromNode(svc), nil
}

// LostUnmanagedKeys returns, sorted, the unmanaged service keys present in
// backup but absent from current. Those are exactly the keys a form-driven
// apply should never remove (build, container_name, network_mode, depends_on,
// ...), so their disappearance is the signature of an apply that overwrote
// more of the file than it should have. A service absent from either file
// yields no loss (that's a create or a delete, not a silent field drop).
func LostUnmanagedKeys(current, backup []byte, service string) ([]string, error) {
	var curRoot, prevRoot yaml.Node
	if err := yaml.Unmarshal(current, &curRoot); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(backup, &prevRoot); err != nil {
		return nil, err
	}
	cur, curOK := serviceNode(&curRoot, service)
	prev, prevOK := serviceNode(&prevRoot, service)
	if !curOK || !prevOK {
		return nil, nil
	}
	curKeys := keysFromNode(cur)
	var lost []string
	for key := range keysFromNode(prev) {
		if ManagedKeys[key] || curKeys[key] {
			continue
		}
		lost = append(lost, key)
	}
	sort.Strings(lost)
	return lost, nil
}

func keysFromNode(svc *yaml.Node) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i+1 < len(svc.Content); i += 2 {
		keys[svc.Content[i].Value] = true
	}
	return keys
}

// serviceNode finds and returns service's value node under the document's
// top-level "services" mapping.
func serviceNode(root *yaml.Node, service string) (*yaml.Node, bool) {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, false
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, false
	}
	services := mappingValue(doc, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, false
	}
	if svc := mappingValue(services, service); svc != nil {
		return svc, true
	}
	return nil, false
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
