package puzzle

import (
	"bytes"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"

	"asmgraph/cell"
	"asmgraph/vm"
)

// MigrateYAML rewrites a schema-v1 puzzle as schema v2 with identical
// behaviour, editing the YAML tree so comments and layout survive:
//
//   - schema_version becomes 2;
//   - cell types renamed since v1 (LegacyTypes) get their new names;
//   - every v1 cell had exactly four ports, so a cell whose type defaults
//     to a different count gets an explicit port_count, unless its enabled
//     ports already fit the default (an input with ports: [P0] simply
//     becomes a one-port input).
//
// It returns the new YAML and a list of the changes. A v2 file is
// returned unchanged.
func MigrateYAML(data []byte, reg *cell.Registry) ([]byte, []string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, nil, err
	}
	doc := docMap(&root)
	if doc == nil {
		return nil, nil, fmt.Errorf("not a YAML mapping")
	}
	ver := mapGet(doc, "schema_version")
	if ver == nil || ver.Value != "1" {
		return data, nil, nil
	}
	ver.Value = strconv.Itoa(SchemaVersion)
	notes := []string{fmt.Sprintf("schema_version: 1 → %d", SchemaVersion)}
	cells := mapGet(doc, "cells")
	if cells != nil && cells.Kind == yaml.SequenceNode {
		for i, c := range cells.Content {
			if c.Kind != yaml.MappingNode {
				continue
			}
			t := mapGet(c, "type")
			if t == nil {
				continue
			}
			if n, ok := LegacyTypes[t.Value]; ok {
				notes = append(notes, fmt.Sprintf("cells[%d].type: %s → %s", i, t.Value, n))
				t.Value = n
			}
			a, ok := reg.Lookup(t.Value)
			if !ok {
				continue
			}
			def := cell.HardwareOf(a).Ports.Default
			if def == vm.LegacyPorts {
				continue
			}
			highest := vm.LegacyPorts - 1 // no list: all four ports enabled
			ports := mapGet(c, "ports")
			if ports != nil && ports.Kind == yaml.SequenceNode {
				highest = -1
				for _, p := range ports.Content {
					if pp, ok := vm.ParsePhysicalPort(p.Value); ok && pp.Index() > highest {
						highest = pp.Index()
					}
				}
			}
			if highest < def {
				if ports != nil && len(ports.Content) == def {
					mapDelete(c, "ports") // every default port is enabled
					notes = append(notes, fmt.Sprintf("cells[%d]: %d port(s), the %s default; ports list removed", i, def, t.Value))
				}
				continue
			}
			mapInsertAfter(c, "type", "port_count", strconv.Itoa(vm.LegacyPorts))
			notes = append(notes, fmt.Sprintf("cells[%d].port_count: %d (v1 cells have four ports)", i, vm.LegacyPorts))
		}
	}
	out, err := encode(&root)
	return out, notes, err
}

func encode(root *yaml.Node) ([]byte, error) {
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

func docMap(root *yaml.Node) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func mapDelete(n *yaml.Node, key string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return
		}
	}
}

func mapInsertAfter(n *yaml.Node, after, key, value string) {
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: value}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == after {
			rest := append([]*yaml.Node{k, v}, n.Content[i+2:]...)
			n.Content = append(n.Content[:i+2], rest...)
			return
		}
	}
	n.Content = append(n.Content, k, v)
}

// MigrateSolutionYAML rewrites a schema-v1 solution as schema v2. The
// solution format only gained fields, so this just updates the version.
func MigrateSolutionYAML(data []byte) ([]byte, []string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, nil, err
	}
	doc := docMap(&root)
	if doc == nil {
		return nil, nil, fmt.Errorf("not a YAML mapping")
	}
	ver := mapGet(doc, "schema_version")
	if ver == nil || ver.Value != "1" {
		return data, nil, nil
	}
	ver.Value = "2"
	out, err := encode(&root)
	return out, []string{"schema_version: 1 → 2"}, err
}

// IsPuzzleYAML reports whether data looks like a puzzle (it has cells or
// tests) rather than a solution.
func IsPuzzleYAML(data []byte) bool {
	var root yaml.Node
	if yaml.Unmarshal(data, &root) != nil {
		return false
	}
	doc := docMap(&root)
	return doc != nil && (mapGet(doc, "cells") != nil || mapGet(doc, "tests") != nil)
}
